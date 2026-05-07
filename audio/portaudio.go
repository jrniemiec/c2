package audio

import (
	"sync"

	"github.com/gordonklaus/portaudio"
)

// paRefs is a reference-counted PortAudio initialisation.
// PortAudio must be initialised exactly once and terminated when no longer needed.
// Multiple callers (capture, playback) share a single init/terminate pair.
var (
	paMu   sync.Mutex
	paRefs int
)

// paInit increments the reference count and calls portaudio.Initialize on the
// first call. Subsequent calls are no-ops (PortAudio is already ready).
func paInit() error {
	paMu.Lock()
	defer paMu.Unlock()
	if paRefs == 0 {
		if err := portaudio.Initialize(); err != nil {
			return err
		}
	}
	paRefs++
	return nil
}

// paTerminate decrements the reference count and calls portaudio.Terminate when
// it reaches zero.
func paTerminate() {
	paMu.Lock()
	defer paMu.Unlock()
	if paRefs <= 0 {
		return
	}
	paRefs--
	if paRefs == 0 {
		portaudio.Terminate()
	}
}

// Init initialises PortAudio for the process. Call once at startup.
// Safe to call multiple times — uses reference counting internally.
func Init() error {
	return paInit()
}

// Terminate releases the PortAudio reference acquired by Init.
func Terminate() {
	paTerminate()
}
