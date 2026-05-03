package speech

import (
	"fmt"
	"strings"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-macos"
)

// STTConfig holds offline (Whisper) speech-to-text configuration.
type STTConfig struct {
	// Whisper model paths
	Encoder string
	Decoder string
	Tokens  string // Path to tokens.txt

	// Common
	Language   string // e.g. "en" (default)
	NumThreads int    // defaults to 2
	SampleRate int    // defaults to 16000
}

func (c *STTConfig) withDefaults() STTConfig {
	out := *c
	if out.NumThreads == 0 {
		out.NumThreads = 2
	}
	if out.SampleRate == 0 {
		out.SampleRate = 16000
	}
	if out.Language == "" {
		out.Language = "en"
	}
	return out
}

// Recognizer wraps a sherpa-onnx offline (Whisper) speech recognizer.
type Recognizer struct {
	impl       *sherpa.OfflineRecognizer
	sampleRate int
}

// NewRecognizer creates an offline STT recognizer from the given config.
func NewRecognizer(cfg STTConfig) (*Recognizer, error) {
	cfg = cfg.withDefaults()

	sherpaConfig := &sherpa.OfflineRecognizerConfig{
		FeatConfig: sherpa.FeatureConfig{
			SampleRate: cfg.SampleRate,
			FeatureDim: 80,
		},
		ModelConfig: sherpa.OfflineModelConfig{
			Whisper: sherpa.OfflineWhisperModelConfig{
				Encoder:  cfg.Encoder,
				Decoder:  cfg.Decoder,
				Language: cfg.Language,
				Task:     "transcribe",
			},
			Tokens:     cfg.Tokens,
			NumThreads: cfg.NumThreads,
			Provider:   "cpu",
			Debug:      0,
		},
		DecodingMethod: "greedy_search",
	}

	impl := sherpa.NewOfflineRecognizer(sherpaConfig)
	if impl == nil {
		return nil, fmt.Errorf("stt: failed to create offline recognizer")
	}
	return &Recognizer{impl: impl, sampleRate: cfg.SampleRate}, nil
}

// Transcribe runs a complete speech segment through the recognizer and
// returns the transcript. samples should come from the VAD.
func (r *Recognizer) Transcribe(samples []float32) (string, error) {
	stream := sherpa.NewOfflineStream(r.impl)
	defer sherpa.DeleteOfflineStream(stream)

	stream.AcceptWaveform(r.sampleRate, samples)
	r.impl.Decode(stream)

	result := stream.GetResult()
	if result == nil {
		return "", nil
	}
	return strings.TrimSpace(result.Text), nil
}

// Close frees the underlying recognizer resources.
func (r *Recognizer) Close() {
	sherpa.DeleteOfflineRecognizer(r.impl)
}
