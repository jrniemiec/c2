package speech

import (
	"fmt"
	"strings"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-macos"
)

// KWSConfig holds KeywordSpotter configuration.
type KWSConfig struct {
	Encoder      string
	Decoder      string
	Joiner       string
	Tokens       string
	KeywordsFile string // path to keywords.txt with token-format lines
	MaxActivePaths int  // beam width; default 4
	NumThreads   int   // default 2
	SampleRate   int   // default 16000
}

func (c *KWSConfig) withDefaults() KWSConfig {
	out := *c
	if out.MaxActivePaths == 0 {
		out.MaxActivePaths = 4
	}
	if out.NumThreads == 0 {
		out.NumThreads = 2
	}
	if out.SampleRate == 0 {
		out.SampleRate = 16000
	}
	return out
}

// KeywordSpotter wraps a sherpa-onnx keyword spotter model.
// One instance is shared across all FSM states; per-state filtering is done
// in the caller via the @label field in the keywords file.
type KeywordSpotter struct {
	impl       *sherpa.KeywordSpotter
	sampleRate int
}

// NewKeywordSpotter creates a KeywordSpotter from the given config.
func NewKeywordSpotter(cfg KWSConfig) (*KeywordSpotter, error) {
	cfg = cfg.withDefaults()
	if cfg.Encoder == "" || cfg.Decoder == "" || cfg.Joiner == "" {
		return nil, fmt.Errorf("kws: encoder, decoder, and joiner paths are required")
	}
	if cfg.KeywordsFile == "" {
		return nil, fmt.Errorf("kws: keywords file path is required")
	}

	sherpaCfg := &sherpa.KeywordSpotterConfig{
		FeatConfig: sherpa.FeatureConfig{
			SampleRate: cfg.SampleRate,
			FeatureDim: 80,
		},
		ModelConfig: sherpa.OnlineModelConfig{
			Transducer: sherpa.OnlineTransducerModelConfig{
				Encoder: cfg.Encoder,
				Decoder: cfg.Decoder,
				Joiner:  cfg.Joiner,
			},
			Tokens:     cfg.Tokens,
			NumThreads: cfg.NumThreads,
			Provider:   "cpu",
		},
		MaxActivePaths:    cfg.MaxActivePaths,
		KeywordsFile:      cfg.KeywordsFile,
		KeywordsScore:     2.0,
		KeywordsThreshold: 0.25,
	}

	impl := sherpa.NewKeywordSpotter(sherpaCfg)
	if impl == nil {
		return nil, fmt.Errorf("kws: failed to create keyword spotter")
	}
	return &KeywordSpotter{impl: impl, sampleRate: cfg.SampleRate}, nil
}

// Close frees the underlying model resources.
func (ks *KeywordSpotter) Close() {
	sherpa.DeleteKeywordSpotter(ks.impl)
}

// KWSStream is a single recognition stream. Create a new stream on every FSM
// state transition to clear partial-token hypotheses from the previous state.
type KWSStream struct {
	impl *sherpa.OnlineStream
	ks   *KeywordSpotter
}

// NewStream creates a fresh KWS recognition stream.
func (ks *KeywordSpotter) NewStream() *KWSStream {
	impl := sherpa.NewKeywordStream(ks.impl)
	return &KWSStream{impl: impl, ks: ks}
}

// Process feeds one audio chunk into the stream and returns the @label of any
// triggered keyword (empty string if nothing fired this chunk).
// The caller should filter the returned label by the active keyword set for
// the current FSM state.
func (s *KWSStream) Process(samples []float32) string {
	if len(samples) == 0 {
		return ""
	}
	s.impl.AcceptWaveform(s.ks.sampleRate, samples)
	for s.ks.impl.IsReady(s.impl) {
		s.ks.impl.Decode(s.impl)
	}
	result := s.ks.impl.GetResult(s.impl)
	if result == nil || result.Keyword == "" {
		return ""
	}
	// Must reset the stream immediately after a keyword fires, per sherpa-onnx docs.
	s.ks.impl.Reset(s.impl)
	// Strip the leading '@' that sherpa-onnx includes for labeled keywords,
	// so callers receive clean labels like "computer" not "@computer".
	return strings.TrimPrefix(result.Keyword, "@")
}

// Reset clears accumulated hypothesis state without discarding encoder context.
// Call this when returning to IDLE after unrecognized speech to prevent stale
// partial hypotheses from interfering with the next keyword detection.
func (s *KWSStream) Reset() {
	s.ks.impl.Reset(s.impl)
}

// Close frees the stream.
func (s *KWSStream) Close() {
	sherpa.DeleteOnlineStream(s.impl)
}
