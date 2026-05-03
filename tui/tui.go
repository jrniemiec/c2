package tui

import (
	"fmt"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jrniemiec/c2/audio"
	"github.com/jrniemiec/c2/c2config"
	"github.com/jrniemiec/c2/speech"
	"github.com/jrniemiec/lore/config"
	"github.com/jrniemiec/lore/engine"
)

var debugLog *log.Logger

func init() {
	f, err := os.OpenFile(os.ExpandEnv("$HOME/.c2/debug.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err == nil {
		debugLog = log.New(f, "", log.Ltime|log.Lmicroseconds)
	}
}

func dbg(format string, args ...any) {
	if debugLog != nil {
		debugLog.Printf(format, args...)
	}
}

// programSend is set by Start() so goroutines can send msgs into the event loop.
var programSend func(tea.Msg)

// Start launches the TUI and blocks until the user quits.
func Start(eng *engine.Engine, cfg config.Config, loreData string, c2cfg c2config.C2Config, theme string, chatLabels bool, foldLines int, foldOnStart bool) error {
	DetectTerminal()
	ApplyTheme(theme)
	AdjustThemeForTerminal()

	fmt.Fprint(os.Stdout, "\033[3J")
	if ActiveTerminal == TermITerm2 {
		fmt.Fprint(os.Stdout, "\033[?1007h")
		defer fmt.Fprint(os.Stdout, "\033[?1007l")
	}

	m := New(eng, cfg, loreData)
	m.themeMode = theme
	m.chatLabels = chatLabels
	m.foldLines = foldLines
	m.foldOnStart = foldOnStart
	m.c2cfg = c2cfg

	// Initialise voice pipeline if models are configured.
	if c2cfg.IsVoiceConfigured() {
		m.voiceReady = true
		m.ttsAuto = true
		m.stopVoice = make(chan struct{})
		go runVoicePipeline(c2cfg, m.stopVoice)
	}

	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if ActiveTerminal != TermITerm2 {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(m, opts...)
	programSend = func(msg tea.Msg) { p.Send(msg) }
	_, err := p.Run()
	programSend = nil

	// Stop voice pipeline on exit.
	if m.stopVoice != nil {
		close(m.stopVoice)
	}
	return err
}

// runVoicePipeline runs the audio capture → VAD → STT loop in a goroutine.
// It sends voiceSpeechStartedMsg and voiceTranscriptMsg into the Bubbletea loop.
func runVoicePipeline(cfg c2config.C2Config, stop <-chan struct{}) {
	send := func(msg interface{}) {
		if programSend != nil {
			programSend(msg)
		}
	}

	// Initialise VAD.
	dbg("pipeline: init VAD model=%s", cfg.VADModel)
	vad, err := speech.NewVAD(speech.VADConfig{
		ModelPath:  cfg.VADModel,
		SampleRate: audio.SampleRate,
	})
	if err != nil {
		send(voicePipelineErrMsg{fmt.Errorf("VAD init: %w", err)})
		return
	}
	defer vad.Close()
	dbg("pipeline: VAD ready")

	// Initialise STT.
	dbg("pipeline: init STT encoder=%s decoder=%s", cfg.STTEncoder, cfg.STTDecoder)
	stt, err := speech.NewRecognizer(speech.STTConfig{
		Encoder:    cfg.STTEncoder,
		Decoder:    cfg.STTDecoder,
		Tokens:     cfg.STTTokens,
		Language:   cfg.STTLanguage,
		SampleRate: audio.SampleRate,
	})
	if err != nil {
		send(voicePipelineErrMsg{fmt.Errorf("STT init: %w", err)})
		return
	}
	defer stt.Close()
	dbg("pipeline: STT ready")

	// Initialise audio capture.
	cap, err := audio.New()
	if err != nil {
		send(voicePipelineErrMsg{fmt.Errorf("audio init: %w", err)})
		return
	}

	audioCh := make(chan []float32, 32)
	capErrCh := make(chan error, 1)
	go func() { capErrCh <- cap.Start(audioCh) }()
	defer cap.Stop()

	for {
		select {
		case <-stop:
			return
		case err := <-capErrCh:
			if err != nil {
				send(voicePipelineErrMsg{fmt.Errorf("capture: %w", err)})
			}
			return
		case chunk, ok := <-audioCh:
			if !ok {
				return
			}
			events := vad.Process(chunk)
			for _, ev := range events {
				if ev.Started {
					dbg("pipeline: speech started")
					send(voiceSpeechStartedMsg{})
				}
				if ev.Segment != nil {
					dbg("pipeline: segment ready, samples=%d", len(ev.Segment))
					text, err := stt.Transcribe(ev.Segment)
					if err != nil {
						dbg("pipeline: STT error: %v", err)
						send(voicePipelineErrMsg{fmt.Errorf("STT: %w", err)})
						continue
					}
					dbg("pipeline: transcript=%q", text)
					send(voiceTranscriptMsg{text: text, final: true})
				}
			}
		}
	}
}
