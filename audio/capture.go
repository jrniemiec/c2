// Package audio handles microphone capture via PortAudio.
package audio

import (
	"fmt"

	"github.com/gordonklaus/portaudio"
)

const (
	SampleRate      = 16000 // Hz — required by sherpa-onnx models
	FramesPerBuffer = 512   // samples per callback
)

// Capturer reads mono float32 audio from the default input device.
type Capturer struct {
	stream *portaudio.Stream
	buf    []float32
}

// New opens the default input device.
// PortAudio must already be initialised via audio.Init().
func New() (*Capturer, error) {
	c := &Capturer{
		buf: make([]float32, FramesPerBuffer),
	}
	return c, nil
}

// Start begins audio capture. Each chunk of FramesPerBuffer float32 samples
// is sent on out. Blocks until Stop() is called or an error occurs.
func (c *Capturer) Start(out chan<- []float32) error {
	var err error
	c.stream, err = portaudio.OpenDefaultStream(
		1,           // input channels (mono)
		0,           // output channels
		SampleRate,
		FramesPerBuffer,
		c.buf,
	)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	if err := c.stream.Start(); err != nil {
		return fmt.Errorf("start stream: %w", err)
	}
	for {
		if err := c.stream.Read(); err != nil {
			if err == portaudio.InputOverflowed {
				// Buffer overrun — drop the chunk and continue.
				continue
			}
			return err
		}
		chunk := make([]float32, FramesPerBuffer)
		copy(chunk, c.buf)
		out <- chunk
	}
}

// Stop halts capture and terminates PortAudio.
func (c *Capturer) Stop() error {
	if c.stream != nil {
		if err := c.stream.Stop(); err != nil {
			return err
		}
		if err := c.stream.Close(); err != nil {
			return err
		}
	}
	return nil
}
