// Package c2config reads the "c2" section from ~/.c2/config.json.
package c2config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// C2Config holds c2-specific settings from the "c2" key in config.json.
type C2Config struct {
	// STT — Whisper model paths
	STTEncoder  string `json:"stt_encoder"`
	STTDecoder  string `json:"stt_decoder"`
	STTTokens   string `json:"stt_tokens"`
	STTLanguage string `json:"stt_language"` // default "en"

	// VAD
	VADModel string `json:"vad_model"`

	// TTS — readout backend (speaking exchange text on demand: s key, replay, play-all)
	// TTSReadoutBackend selects the engine: "kokoro" (sherpa-onnx) or "say" (macOS).
	TTSReadoutBackend   string  `json:"tts_readout_backend"`    // "kokoro" or "say"
	TTSReadoutSpeed     float32 `json:"tts_readout_speed"`      // wpm; mapped to kokoro speed as wpm/200
	TTSReadoutVoice     string  `json:"tts_readout_voice"`      // kokoro: lang tag (e.g. "en-us"); say: voice name
	TTSReadoutSpeakerID int     `json:"tts_readout_speaker_id"` // kokoro speaker index (default 0)

	// TTS — command backend (acks and short responses: "Deleted", "Conversing", etc.)
	// Always uses say(1).
	TTSCommandSpeed float32 `json:"tts_command_speed"` // wpm for say (default 200)
	TTSCommandVoice string  `json:"tts_command_voice"` // say voice name (empty = system default)

	// TTS — Kokoro model paths (used when TTSReadoutBackend == "kokoro")
	TTSModel   string `json:"tts_model"`    // path to model.onnx
	TTSVoices  string `json:"tts_voices"`   // path to voices.bin
	TTSTokens  string `json:"tts_tokens"`   // path to tokens.txt
	TTSDataDir string `json:"tts_data_dir"` // path to espeak-ng-data directory
	TTSLexicon string `json:"tts_lexicon"`  // optional lexicon file path

	// KWS — KeywordSpotter (transducer) model paths
	KWSEncoder  string  `json:"kws_encoder"`
	KWSDecoder  string  `json:"kws_decoder"`
	KWSJoiner   string  `json:"kws_joiner"`
	KWSTokens   string  `json:"kws_tokens"`
	KWSKeywords string  `json:"kws_keywords"` // path to c2_keywords.txt
	KWSGain     float32 `json:"kws_gain"`     // amplitude multiplier for KWS input (0 = disabled)

	// Audio
	InputDevice string `json:"input_device"`

	// Input correction (Ctrl+R)
	// CorrectionProfile selects which profile to use for Ctrl+R corrections.
	// Defaults to "oai-mini" if empty.
	CorrectionProfile string `json:"correction_profile"`
	// CorrectionPrompt overrides the built-in system prompt used for corrections.
	// Leave empty to use the default.
	CorrectionPrompt string `json:"correction_prompt"`
}

// Load reads the c2 section from the config file at path.
// Missing file or missing "c2" key returns a zero-value C2Config without error.
func Load(configPath string) (C2Config, error) {
	b, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return C2Config{}, nil
		}
		return C2Config{}, fmt.Errorf("c2config: read %s: %w", configPath, err)
	}

	var raw struct {
		C2 C2Config `json:"c2"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return C2Config{}, fmt.Errorf("c2config: parse %s: %w", configPath, err)
	}

	cfg := raw.C2
	cfg.expand()
	return cfg, nil
}

// expand resolves ~ in all path fields.
func (c *C2Config) expand() {
	home, _ := os.UserHomeDir()
	expandPath := func(p string) string {
		if strings.HasPrefix(p, "~/") {
			return filepath.Join(home, p[2:])
		}
		return p
	}
	c.STTEncoder = expandPath(c.STTEncoder)
	c.STTDecoder = expandPath(c.STTDecoder)
	c.STTTokens = expandPath(c.STTTokens)
	c.VADModel = expandPath(c.VADModel)
	c.TTSModel = expandPath(c.TTSModel)
	c.TTSVoices = expandPath(c.TTSVoices)
	c.TTSTokens = expandPath(c.TTSTokens)
	c.TTSDataDir = expandPath(c.TTSDataDir)
	c.TTSLexicon = expandPath(c.TTSLexicon)
	c.KWSEncoder = expandPath(c.KWSEncoder)
	c.KWSDecoder = expandPath(c.KWSDecoder)
	c.KWSJoiner = expandPath(c.KWSJoiner)
	c.KWSTokens = expandPath(c.KWSTokens)
	c.KWSKeywords = expandPath(c.KWSKeywords)
}

// IsVoiceConfigured returns true if the minimum required model paths are set.
func (c *C2Config) IsVoiceConfigured() bool {
	return c.VADModel != "" && c.STTEncoder != "" && c.STTDecoder != ""
}

// IsKWSConfigured returns true if all KWS model paths are set.
func (c *C2Config) IsKWSConfigured() bool {
	return c.KWSEncoder != "" && c.KWSDecoder != "" && c.KWSJoiner != "" &&
		c.KWSTokens != "" && c.KWSKeywords != ""
}
