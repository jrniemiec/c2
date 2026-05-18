package speech

// kokoro_stream.go — Kokoro-specific TTS: clause splitting + PortAudio playback.
//
// All Kokoro streaming logic lives here. To disable Kokoro entirely, change one
// line in tui/update.go:startTTS to call startTTSSay instead.
//
// GenerateStreaming splits text into short clauses via ExtractClauses before
// calling Kokoro, so each inference call is ~10-15 words (~0.5-1s) regardless
// of whether the caller is streaming tokens or replaying a full exchange.

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gordonklaus/portaudio"

	"github.com/jrniemiec/c2/audio"
)

const kokoroPlaybackChunk = 1024

// GenerateStreaming runs Kokoro inference on the full text and plays the result
// via PortAudio. stopCh can be closed to abort early (e.g. killTTS).
// speed: Kokoro multiplier (1.0 = normal; map from wpm: speed = wpm/200.0).
func (e *TTSEngine) GenerateStreaming(text string, speakerID int, speed float32, stopCh <-chan struct{}) error {
	if speed <= 0 {
		speed = 1.0
	}
	t0 := time.Now()
	result := e.impl.Generate(text, speakerID, speed)
	if result == nil {
		return fmt.Errorf("kokoro: synthesis returned nil")
	}
	slog.Debug("kokoro stream: inferred", "inference_ms", time.Since(t0).Milliseconds(), "samples", len(result.Samples))
	return kokoroPlay(result.Samples, e.SampleRate, stopCh)
}

// kokoroPlay plays PCM samples through PortAudio, stopping early if stopCh closes.
func kokoroPlay(samples []float32, sampleRate int, stopCh <-chan struct{}) error {
	audio.PlayMu.Lock()
	defer audio.PlayMu.Unlock()

	buf := make([]float32, kokoroPlaybackChunk)
	stream, err := portaudio.OpenDefaultStream(0, 1, float64(sampleRate), kokoroPlaybackChunk, buf)
	if err != nil {
		return fmt.Errorf("kokoro play: open: %w", err)
	}
	defer stream.Close()
	if err := stream.Start(); err != nil {
		return fmt.Errorf("kokoro play: start: %w", err)
	}
	defer stream.Stop()

	for offset := 0; offset < len(samples); offset += kokoroPlaybackChunk {
		select {
		case <-stopCh:
			return nil
		default:
		}
		end := offset + kokoroPlaybackChunk
		if end > len(samples) {
			end = len(samples)
		}
		copy(buf, samples[offset:end])
		for i := end - offset; i < kokoroPlaybackChunk; i++ {
			buf[i] = 0
		}
		if err := stream.Write(); err != nil && err != portaudio.OutputUnderflowed {
			return fmt.Errorf("kokoro play: write: %w", err)
		}
	}
	return nil
}

// ExtractClauses is a Kokoro-specific variant of sentence splitting that also
// splits at clause boundaries (,;:—) to keep each chunk short (~10-15 words).
// Rollback: in update.go, replace speech.ExtractClauses with extractSentences.
const KokoroMinClauseLen = 12

func ExtractClauses(s string) (clauses []string, remainder string) {
	runes := []rune(s)
	n := len(runes)
	start := 0

	advance := func(after int) {
		i := after
		for i < n && (runes[i] == ' ' || runes[i] == '\n' || runes[i] == '\t') {
			i++
		}
		start = i
	}

	emit := func(end int) bool {
		candidate := strings.TrimSpace(string(runes[start : end+1]))
		if len([]rune(candidate)) >= KokoroMinClauseLen {
			clauses = append(clauses, candidate)
			advance(end + 1)
			return true
		}
		return false
	}

	for i := 0; i < n; i++ {
		ch := runes[i]

		switch ch {
		case '.', '!', '?':
			if ch == '.' && i > 0 {
				prev := runes[i-1]
				if prev >= '0' && prev <= '9' {
					continue
				}
				if prev >= 'A' && prev <= 'Z' && (i < 2 || runes[i-2] == ' ' || runes[i-2] == '.') {
					continue
				}
			}
			end := i
			for end+1 < n && (runes[end+1] == '.' || runes[end+1] == '!' || runes[end+1] == '?') {
				end++
			}
			if end+1 < n && runes[end+1] != ' ' && runes[end+1] != '\n' && runes[end+1] != '\t' {
				i = end
				continue
			}
			if emit(end) {
				i = start - 1
			}

		case ',', ';', ':':
			if i+1 < n && (runes[i+1] == ' ' || runes[i+1] == '\n') {
				if emit(i) {
					i = start - 1
				}
			}

		case '—', '–':
			if i > start {
				if emit(i - 1) {
					// Skip the dash and trailing space.
					for start < n && (runes[start] == '—' || runes[start] == '–' || runes[start] == ' ') {
						start++
					}
					i = start - 1
				}
			}
		}
	}

	remainder = string(runes[start:])
	return
}
