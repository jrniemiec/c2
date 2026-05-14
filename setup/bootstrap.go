package setup

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/jrniemiec/c2/config"
	"github.com/jrniemiec/c2/internal/clog"
)

// PromptEditConfig tells the user to edit the config, then loops until the
// config reloads and passes basic validation. Returns the valid config.
func PromptEditConfig(cfgPath string) (config.Config, error) {
	fmt.Fprintf(os.Stderr, "c2:\n")
	fmt.Fprintf(os.Stderr, "c2: API keys are read from environment variables:\n")
	fmt.Fprintf(os.Stderr, "c2:   Anthropic  — ANTHROPIC_API_KEY  (or C2_ANTHROPIC_API_KEY)\n")
	fmt.Fprintf(os.Stderr, "c2:   OpenAI     — OPENAI_API_KEY     (or C2_OPENAI_API_KEY)\n")
	fmt.Fprintf(os.Stderr, "c2:   Ollama     — no key needed, set C2_OLLAMA_HOST if not localhost\n")
	fmt.Fprintf(os.Stderr, "c2:\n")
	fmt.Fprintf(os.Stderr, "c2: → edit %s\n", cfgPath)
	fmt.Fprintf(os.Stderr, "c2:   make sure you set the default LLM profile and default topic to the values you want\n")

	r := bufio.NewReader(os.Stdin)
	for {
		fmt.Fprint(os.Stderr, "c2: press Enter when ready to continue (or q to quit): ")
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "q" || line == "quit" {
			return config.Config{}, fmt.Errorf("aborted by user")
		}

		cfg, err := config.Load(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "c2: config error: %v — fix the file and try again\n", err)
			clog.Warnf("bootstrap: config load error: %v", err)
			continue
		}
		if err := validateConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "c2: %v — fix the file and try again\n", err)
			clog.Warnf("bootstrap: config validation error: %v", err)
			continue
		}
		clog.Infof("bootstrap: config validated OK, default_profile=%s", cfg.DefaultProfile)
		return cfg, nil
	}
}

// PromptComplete reports that bootstrapping is done and waits for the user to confirm before continuing.
// Returns false if the user chose to quit.
func PromptComplete() bool {
	clog.Infof("bootstrap: voice setup complete")
	fmt.Fprintln(os.Stderr, "c2: bootstrap complete — all models downloaded and config written")
	fmt.Fprint(os.Stderr, "c2: continue? [Y/n]: ")
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "" || line == "y" || line == "yes"
}

// validateConfig runs basic sanity checks on the loaded config.
func validateConfig(cfg config.Config) error {
	if cfg.DefaultProfile == "" {
		return fmt.Errorf("default_profile is not set in config")
	}
	if _, ok := cfg.Profiles[cfg.DefaultProfile]; !ok {
		return fmt.Errorf("default_profile %q not found in profiles", cfg.DefaultProfile)
	}
	return nil
}
