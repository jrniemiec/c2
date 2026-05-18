package audio

import (
	"fmt"
	"sync"

	"github.com/gordonklaus/portaudio"
)

const playbackChunk = 1024 // samples per write; controls interrupt granularity

// PlayMu ensures only one output stream is open at a time.
// Concurrent calls to Play (e.g. beep + TTS) would cause Core Audio -50 errors.
// Exported so that kokoro_stream.go can acquire it for the duration of streaming playback.
var PlayMu sync.Mutex

// Play writes mono float32 PCM samples to the default output device at the
// given sample rate. It returns early (without error) if stop is closed.
// PortAudio must already be initialised via audio.Init().
func Play(samples []float32, sampleRate int, stop <-chan struct{}) error {
	PlayMu.Lock()
	defer PlayMu.Unlock()
	buf := make([]float32, playbackChunk)
	stream, err := portaudio.OpenDefaultStream(
		0,          // no input
		1,          // mono output
		float64(sampleRate),
		playbackChunk,
		buf,
	)
	if err != nil {
		return fmt.Errorf("playback: open stream: %w", err)
	}
	defer stream.Close()

	if err := stream.Start(); err != nil {
		return fmt.Errorf("playback: start stream: %w", err)
	}
	defer stream.Stop()

	for offset := 0; offset < len(samples); offset += playbackChunk {
		select {
		case <-stop:
			return nil
		default:
		}
		end := offset + playbackChunk
		if end > len(samples) {
			end = len(samples)
		}
		copy(buf, samples[offset:end])
		// Zero-pad the last chunk if it's shorter than the buffer.
		for i := end - offset; i < playbackChunk; i++ {
			buf[i] = 0
		}
		if err := stream.Write(); err != nil && err != portaudio.OutputUnderflowed {
			return fmt.Errorf("playback: write: %w", err)
		}
	}
	return nil
}
