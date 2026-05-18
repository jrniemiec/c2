package speech

import (
	"fmt"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-macos"
)

// TTSEngineConfig holds Kokoro TTS model configuration.
type TTSEngineConfig struct {
	Model   string // path to model.onnx
	Voices  string // path to voices.bin
	Tokens  string // path to tokens.txt
	DataDir string // path to espeak-ng-data directory
	Lexicon string // optional lexicon file path
	Lang    string // language tag, e.g. "en-us" (empty = model default)
}

// TTSEngine wraps the sherpa-onnx Kokoro offline TTS.
type TTSEngine struct {
	impl       *sherpa.OfflineTts
	SampleRate int
}

// NewTTSEngine initialises a Kokoro TTS engine from the given config.
func NewTTSEngine(cfg TTSEngineConfig) (*TTSEngine, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("tts: model path is required")
	}
	ttsConfig := &sherpa.OfflineTtsConfig{
		Model: sherpa.OfflineTtsModelConfig{
			Kokoro: sherpa.OfflineTtsKokoroModelConfig{
				Model:       cfg.Model,
				Voices:      cfg.Voices,
				Tokens:      cfg.Tokens,
				DataDir:     cfg.DataDir,
				Lexicon:     cfg.Lexicon,
				Lang:        cfg.Lang,
				LengthScale: 0.85,
			},
			NumThreads: 4,
			Provider:   "cpu",
		},
		MaxNumSentences: 1,
	}
	impl := sherpa.NewOfflineTts(ttsConfig)
	if impl == nil {
		return nil, fmt.Errorf("tts: failed to create OfflineTts engine")
	}
	return &TTSEngine{
		impl:       impl,
		SampleRate: impl.SampleRate(),
	}, nil
}

// Generate synthesises text and returns the raw float32 PCM samples.
// speakerID selects the voice (0 = default). speed is a multiplier (1.0 = normal).
func (e *TTSEngine) Generate(text string, speakerID int, speed float32) ([]float32, error) {
	if speed <= 0 {
		speed = 1.0
	}
	audio := e.impl.Generate(text, speakerID, speed)
	if audio == nil {
		return nil, fmt.Errorf("tts: synthesis returned nil")
	}
	return audio.Samples, nil
}

// Close frees the underlying engine resources.
func (e *TTSEngine) Close() {
	sherpa.DeleteOfflineTts(e.impl)
}
