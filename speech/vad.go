// Package speech wraps sherpa-onnx for VAD, STT, and TTS.
package speech

import (
	"fmt"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-macos"
)

// VADConfig holds Silero VAD configuration.
type VADConfig struct {
	ModelPath          string
	Threshold          float32 // speech probability threshold (default 0.5)
	MinSilenceDuration float32 // seconds of silence to end a segment (default 0.5)
	MinSpeechDuration  float32 // minimum speech duration in seconds (default 0.25)
	MaxSpeechDuration  float32 // maximum speech segment in seconds (default 10.0)
	SampleRate         int
}

func (c *VADConfig) withDefaults() VADConfig {
	out := *c
	if out.Threshold == 0 {
		out.Threshold = 0.5
	}
	if out.MinSilenceDuration == 0 {
		out.MinSilenceDuration = 0.5
	}
	if out.MinSpeechDuration == 0 {
		out.MinSpeechDuration = 0.25
	}
	if out.MaxSpeechDuration == 0 {
		out.MaxSpeechDuration = 10.0
	}
	if out.SampleRate == 0 {
		out.SampleRate = 16000
	}
	return out
}

// VAD wraps the sherpa-onnx Silero Voice Activity Detector.
type VAD struct {
	impl       *sherpa.VoiceActivityDetector
	sampleRate int
	isSpeech   bool // tracks last known speech state
}

// NewVAD creates a VAD from the given config.
func NewVAD(cfg VADConfig) (*VAD, error) {
	cfg = cfg.withDefaults()
	if cfg.ModelPath == "" {
		return nil, fmt.Errorf("vad: model path is required")
	}
	sherpaConfig := &sherpa.VadModelConfig{
		SileroVad: sherpa.SileroVadModelConfig{
			Model:              cfg.ModelPath,
			Threshold:          cfg.Threshold,
			MinSilenceDuration: cfg.MinSilenceDuration,
			MinSpeechDuration:  cfg.MinSpeechDuration,
			MaxSpeechDuration:  cfg.MaxSpeechDuration,
			WindowSize:         512,
		},
		SampleRate: cfg.SampleRate,
		NumThreads: 1,
		Provider:   "cpu",
	}
	const bufferSecs = 30
	impl := sherpa.NewVoiceActivityDetector(sherpaConfig, bufferSecs)
	if impl == nil {
		return nil, fmt.Errorf("vad: failed to create voice activity detector")
	}
	return &VAD{impl: impl, sampleRate: cfg.SampleRate}, nil
}

// SpeechEvent is emitted by Process when voice state changes.
type SpeechEvent struct {
	Started  bool     // true = speech onset detected
	Segment  []float32 // non-nil when a complete utterance is ready (silence detected)
}

// Process feeds audio samples into the VAD and returns any events.
// samples must be mono float32 at the configured sample rate.
func (v *VAD) Process(samples []float32) []SpeechEvent {
	v.impl.AcceptWaveform(samples)

	var events []SpeechEvent

	// Detect speech onset.
	speaking := v.impl.IsSpeech()
	if speaking && !v.isSpeech {
		events = append(events, SpeechEvent{Started: true})
	}
	v.isSpeech = speaking

	// Drain any completed segments (silence detected after speech).
	for !v.impl.IsEmpty() {
		seg := v.impl.Front()
		v.impl.Pop()
		events = append(events, SpeechEvent{Segment: seg.Samples})
	}

	return events
}

// Reset clears internal VAD state (e.g. on mode switch).
func (v *VAD) Reset() {
	v.impl.Reset()
	v.isSpeech = false
}

// Close frees the underlying VAD resources.
func (v *VAD) Close() {
	sherpa.DeleteVoiceActivityDetector(v.impl)
}
