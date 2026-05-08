package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed config.default.json
var defaultConfigJSON []byte

const (
	defaultTopicName  = "default"
	defaultWindowMsgs = 1024
)

// ProviderProfile defines a named LLM configuration.
type ProviderProfile struct {
	Provider          string         `json:"provider"`
	Host              string         `json:"host,omitempty"`
	Model             string         `json:"model"`
	MaxContextTokens  int            `json:"max_context_tokens,omitempty"`
	ContextTokenLimit int            `json:"context_token_limit,omitempty"`
	MaxUserMessages   int            `json:"max_user_messages,omitempty"`
	MaxOutputTokens   int            `json:"max_output_tokens,omitempty"`
	Strategy          string         `json:"strategy,omitempty"`
	SummarizerProfile string         `json:"summarizer_profile,omitempty"`
	VerbatimRatio     float64        `json:"verbatim_ratio,omitempty"`
	Color             string         `json:"color,omitempty"`
	Info              map[string]any `json:"info,omitempty"`
}

// Config is the top-level c2 configuration.
type Config struct {
	TopicsRoot     string                     `json:"topics_root"`
	DefaultTopic   string                     `json:"default_topic,omitempty"`
	DefaultProfile string                     `json:"default_profile,omitempty"`
	WindowMessages int                        `json:"window_messages,omitempty"`
	Profiles       map[string]ProviderProfile `json:"profiles"`

	// Display preferences
	ChatLabels  *bool `json:"chat_labels,omitempty"`  // nil = use default (true)
	FoldLines   int   `json:"fold_lines,omitempty"`   // 0 = use default (20)
	FoldOnStart *bool `json:"fold_on_start,omitempty"` // nil = use default (false)
}

// DataDir returns the c2 data directory ($C2_DATA or ~/.c2).
func DataDir() string {
	if h := os.Getenv("C2_DATA"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".c2"
	}
	return filepath.Join(home, ".c2")
}

// DefaultConfigPath returns the path to c2's config.json.
func DefaultConfigPath() string {
	return filepath.Join(DataDir(), "config.json")
}

// DefaultTopicsRoot returns the default topics directory.
func DefaultTopicsRoot() string {
	return filepath.Join(DataDir(), "topics")
}

// Load reads config from path. Missing file returns safe defaults.
func Load(path string) (Config, error) {
	cfg := Config{
		TopicsRoot:     DefaultTopicsRoot(),
		DefaultTopic:   defaultTopicName,
		WindowMessages: defaultWindowMsgs,
		Profiles:       map[string]ProviderProfile{},
	}
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, err
	}
	if len(b) == 0 {
		return cfg, nil
	}
	var loaded Config
	if err := json.Unmarshal(b, &loaded); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	mergeConfig(&cfg, loaded)
	cfg.TopicsRoot = filepath.Clean(cfg.TopicsRoot)
	return cfg, nil
}

func mergeConfig(dst *Config, src Config) {
	if strings.TrimSpace(src.TopicsRoot) != "" {
		dst.TopicsRoot = src.TopicsRoot
	}
	if strings.TrimSpace(src.DefaultTopic) != "" {
		dst.DefaultTopic = src.DefaultTopic
	}
	if strings.TrimSpace(src.DefaultProfile) != "" {
		dst.DefaultProfile = src.DefaultProfile
	}
	if src.WindowMessages > 0 {
		dst.WindowMessages = src.WindowMessages
	}
	if src.Profiles != nil {
		if dst.Profiles == nil {
			dst.Profiles = map[string]ProviderProfile{}
		}
		for code, prof := range src.Profiles {
			if strings.TrimSpace(code) != "" {
				dst.Profiles[code] = prof
			}
		}
	}
	if src.ChatLabels != nil {
		dst.ChatLabels = src.ChatLabels
	}
	if src.FoldLines > 0 {
		dst.FoldLines = src.FoldLines
	}
	if src.FoldOnStart != nil {
		dst.FoldOnStart = src.FoldOnStart
	}
}

// SaveAtomic writes cfg to path atomically (temp file + rename).
// Unknown top-level keys already present in the file (e.g. a "c2" section
// added by a sibling tool) are preserved unchanged.
func SaveAtomic(path string, cfg Config) error {
	// Seed the raw map from the existing file so unknown keys are preserved.
	rawMap := make(map[string]json.RawMessage)
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &rawMap) // best-effort; start fresh if corrupt
	}

	// Marshal cfg and merge its fields into rawMap, overwriting known keys.
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	var cfgFields map[string]json.RawMessage
	if err := json.Unmarshal(b, &cfgFields); err != nil {
		return err
	}
	for k, v := range cfgFields {
		rawMap[k] = v
	}

	out, err := json.MarshalIndent(rawMap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Bootstrap creates the c2 data directory on first run.
// Returns true if bootstrap ran (caller should exit after notifying the user).
// No-op if dataDir already exists.
func Bootstrap(dataDir string) (bool, error) {
	if _, err := os.Stat(dataDir); err == nil {
		return false, nil // already exists
	}

	topicsRoot := filepath.Join(dataDir, "topics")
	defaultTopicDir := filepath.Join(topicsRoot, defaultTopicName)
	cfgPath := filepath.Join(dataDir, "config.json")

	// Create directory structure.
	if err := os.MkdirAll(defaultTopicDir, 0755); err != nil {
		return false, fmt.Errorf("create data dir: %w", err)
	}

	// Load embedded default config, fill in topics_root, write it out.
	var cfg Config
	if err := json.Unmarshal(defaultConfigJSON, &cfg); err != nil {
		return false, fmt.Errorf("parse default config: %w", err)
	}
	cfg.TopicsRoot = topicsRoot
	if err := SaveAtomic(cfgPath, cfg); err != nil {
		return false, fmt.Errorf("write config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "c2: first run — created %s\n", dataDir)
	fmt.Fprintf(os.Stderr, "c2: created %s\n", defaultTopicDir)
	fmt.Fprintf(os.Stderr, "c2: copied default config to %s\n", cfgPath)
	fmt.Fprintf(os.Stderr, "c2: → edit %s to set your default profile, then run c2 again.\n", cfgPath)
	fmt.Fprintf(os.Stderr, "c2:\n")
	fmt.Fprintf(os.Stderr, "c2: API keys are read from environment variables:\n")
	fmt.Fprintf(os.Stderr, "c2:   Anthropic  — ANTHROPIC_API_KEY  (or C2_ANTHROPIC_API_KEY)\n")
	fmt.Fprintf(os.Stderr, "c2:   OpenAI     — OPENAI_API_KEY     (or C2_OPENAI_API_KEY)\n")
	fmt.Fprintf(os.Stderr, "c2:   Ollama     — no key needed, set C2_OLLAMA_HOST if not localhost\n")

	return true, nil
}

// ResolveProfile returns the profile for code (falls back to DefaultProfile).
func ResolveProfile(cfg Config, code string) (string, ProviderProfile, error) {
	if code == "" {
		code = cfg.DefaultProfile
	}
	if code == "" {
		return "", ProviderProfile{}, fmt.Errorf("no profile selected: set --profile or config default_profile")
	}
	p, ok := cfg.Profiles[code]
	if !ok {
		return "", ProviderProfile{}, fmt.Errorf("unknown profile %q", code)
	}
	if p.Provider == "" {
		return "", ProviderProfile{}, fmt.Errorf("profile %q missing provider", code)
	}
	if p.Model == "" {
		return "", ProviderProfile{}, fmt.Errorf("profile %q missing model", code)
	}
	return code, p, nil
}

// EffectiveTopic returns the active topic name.
func EffectiveTopic(cfg Config, flagTopic string) string {
	if flagTopic != "" {
		return flagTopic
	}
	if cfg.DefaultTopic != "" {
		return cfg.DefaultTopic
	}
	return defaultTopicName
}
