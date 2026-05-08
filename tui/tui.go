package tui

import (
	"fmt"
	"log"
	"math"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jrniemiec/c2/audio"
	"github.com/jrniemiec/c2/c2config"
	"github.com/jrniemiec/c2/speech"
	"github.com/jrniemiec/c2/config"
	"github.com/jrniemiec/c2/engine"
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

// activeC2Cfg holds the c2 config so goroutines can access TTS settings.
var activeC2Cfg c2config.C2Config

// Start launches the TUI and blocks until the user quits.
func Start(eng *engine.Engine, cfg config.Config, loreData string, c2cfg c2config.C2Config, theme string, chatLabels bool, foldLines int, foldOnStart bool, textMode bool) error {
	DetectTerminal()
	ApplyTheme(theme)
	AdjustThemeForTerminal()

	fmt.Fprint(os.Stdout, "\033[3J")
	if ActiveTerminal == TermITerm2 {
		fmt.Fprint(os.Stdout, "\033[?1007h")
		defer fmt.Fprint(os.Stdout, "\033[?1007l")
	}

	activeC2Cfg = c2cfg

	m := New(eng, cfg, loreData)
	m.themeMode = theme
	m.chatLabels = chatLabels
	m.foldLines = foldLines
	m.foldOnStart = foldOnStart
	m.c2cfg = c2cfg

	// Initialise voice pipeline if models are configured and not in text-only mode.
	voiceDone := make(chan struct{})
	close(voiceDone) // pre-closed so the wait below is a no-op when voice is disabled
	voiceEnabled := false
	if !textMode && c2cfg.IsVoiceConfigured() {
		// Initialise PortAudio once for the process lifetime.
		if err := audio.Init(); err != nil {
			return fmt.Errorf("portaudio init: %w", err)
		}
		voiceEnabled = true

		m.voiceReady = true
		m.ttsAuto = true
		m.stopVoice = make(chan struct{})
		m.stateChangeCh = make(chan VoiceState, 1)
		m.mode = modeVoice
		m.voiceState = VoiceIdle
		m.voiceFlushCh = make(chan struct{}, 1)
		m.stateChangeCh <- VoiceIdle

		voiceDone = make(chan struct{})
		go func() {
			defer close(voiceDone)
			runVoicePipeline(c2cfg, m.stopVoice, m.stateChangeCh, m.voiceFlushCh)
		}()
	}

	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if ActiveTerminal != TermITerm2 {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(m, opts...)
	programSend = func(msg tea.Msg) { p.Send(msg) }
	finalModel, err := p.Run()
	programSend = nil

	// Kill any in-flight TTS before tearing down audio.
	if fm, ok := finalModel.(Model); ok {
		fm.killTTS()
	}

	// Signal the voice pipeline to stop and wait for it to fully exit
	// before terminating PortAudio — avoids segfault from CGo use-after-free.
	if m.stopVoice != nil {
		close(m.stopVoice)
	}
	<-voiceDone

	if voiceEnabled {
		audio.Terminate()
	}
	return err
}

// runVoicePipeline runs audio capture → KWS (always) → VAD+STT (gated by FSM state).
// KWS keyword events are sent as voiceKWSEventMsg.
// VAD+STT transcript events are sent as voiceTranscriptMsg (only in Dictating/Conversing).
func runVoicePipeline(cfg c2config.C2Config, stop <-chan struct{}, stateChangeCh <-chan VoiceState, flushCh <-chan struct{}) {
	send := func(msg interface{}) {
		if programSend != nil {
			programSend(msg)
		}
	}

	// Initialise KWS if configured.
	var kws *speech.KeywordSpotter
	var kwsStream *speech.KWSStream
	if cfg.IsKWSConfigured() {
		dbg("pipeline: init KWS encoder=%s", cfg.KWSEncoder)
		var err error
		kws, err = speech.NewKeywordSpotter(speech.KWSConfig{
			Encoder:      cfg.KWSEncoder,
			Decoder:      cfg.KWSDecoder,
			Joiner:       cfg.KWSJoiner,
			Tokens:       cfg.KWSTokens,
			KeywordsFile: cfg.KWSKeywords,
		})
		if err != nil {
			send(voicePipelineErrMsg{fmt.Errorf("KWS init: %w", err)})
			return
		}
		defer kws.Close()
		kwsStream = kws.NewStream()
		// Use a closure so the defer sees the current value of kwsStream at
		// cleanup time. A plain `defer kwsStream.Close()` would capture the
		// pointer value immediately; if the loop calls Close()+nil later, the
		// deferred call would double-free the same C object.
		defer func() {
			if kwsStream != nil {
				kwsStream.Close()
			}
		}()
		dbg("pipeline: KWS ready")
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

	currentState := VoiceIdle
	computerPending := false // true after "computer" is sent, until TUI acknowledges via stateChangeCh
	levelChunkCount := 0    // throttle: send voiceLevelMsg every 3 chunks (~96ms)

	// Audio accumulator used in AWAKE, DICTATING, and CONVERSING states.
	// VAD is bypassed — we accumulate raw audio and flush on "over" KWS or a
	// hard cap. In AWAKE we also flush on silence (short command utterances).
	const (
		speechSilenceThreshold = 0.01   // RMS below this counts as silence
		awakeSilenceSamples    = 12000  // 750ms @ 16kHz — flush command on silence
		awakeMaxSamples        = 64000  // 4s hard cap for commands
		speechMaxSamples       = 240000 // 15s hard cap for dictation/conversation
		speechTrailSamples     = 3200   // 200ms margin kept after last active speech
	)
	var speechBuf []float32
	var speechSpeechSeen bool
	var speechSilenceCount int
	var speechLastActiveSample int

	for {
		select {
		case <-stop:
			return

		case newState := <-stateChangeCh:
			dbg("pipeline: state %s → %s", currentState, newState)
			computerPending = false
			currentState = newState
			// Reset audio accumulator on every state change.
			speechBuf = speechBuf[:0]
			speechSpeechSeen = false
			speechSilenceCount = 0
			speechLastActiveSample = 0
			// Do NOT recreate the KWS stream on state changes — the stream's
			// encoder context (left-64 = 640ms) is valuable and must be preserved
			// so the next keyword can be detected immediately. Hypothesis state is
			// already cleared by the Reset() call inside Process() after each keyword
			// fires. We only recreate the stream when pausing (to release resources)
			// and restore it when resuming.
			if newState == VoicePaused && kwsStream != nil {
				kwsStream.Close()
				kwsStream = nil
			} else if newState == VoiceIdle && kwsStream == nil && kws != nil {
				kwsStream = kws.NewStream()
			} else if newState == VoiceIdle && kwsStream != nil {
				// Clear stale hypotheses from unrecognized speech in AWAKE state,
				// but keep encoder context so the next wake word is detected quickly.
				kwsStream.Reset()
			}
			vad.Reset() // reset VAD state on every transition

		case <-flushCh:
			// "over" keyword: flush accumulated audio immediately.
			if speechSpeechSeen && len(speechBuf) > 0 {
				end := speechLastActiveSample + speechTrailSamples
				if end > len(speechBuf) {
					end = len(speechBuf)
				}
				buf := make([]float32, end)
				copy(buf, speechBuf[:end])
				speechBuf = speechBuf[:0]
				speechSpeechSeen = false
				speechSilenceCount = 0
				speechLastActiveSample = 0
				dbg("pipeline: over-flush state=%s samples=%d", currentState, len(buf))
				text, err := stt.Transcribe(buf)
				if err != nil {
					dbg("pipeline: STT error: %v", err)
					send(voicePipelineErrMsg{fmt.Errorf("STT: %w", err)})
				} else {
					dbg("pipeline: transcript=%q", text)
					send(voiceTranscriptMsg{text: text, final: true})
				}
			}

		case err := <-capErrCh:
			if err != nil {
				send(voicePipelineErrMsg{fmt.Errorf("capture: %w", err)})
			}
			return

		case chunk, ok := <-audioCh:
			if !ok {
				return
			}

			// Drop all audio when in text mode.
			if currentState == VoicePaused {
				continue
			}

			// KWS — always active in voice mode.
			if kwsStream != nil {
				kwsChunk := chunk
				if cfg.KWSGain > 0 && cfg.KWSGain != 1.0 {
					kwsChunk = make([]float32, len(chunk))
					for i, s := range chunk {
						v := s * cfg.KWSGain
						if v > 1.0 {
							v = 1.0
						} else if v < -1.0 {
							v = -1.0
						}
						kwsChunk[i] = v
					}
				}
				if label := kwsStream.Process(kwsChunk); label != "" {
					dbg("pipeline: KWS keyword=%q state=%s", label, currentState)
					if label == "computer" {
						if computerPending {
							dbg("pipeline: computer ignored (pending acknowledgement)")
						} else {
							computerPending = true
							send(voiceKWSEventMsg{keyword: label})
						}
					} else {
						send(voiceKWSEventMsg{keyword: label})
					}
				}
			}

			// Mic level — compute RMS and send throttled update to UI.
			levelChunkCount++
			if levelChunkCount >= 3 {
				levelChunkCount = 0
				var sum float32
				for _, s := range chunk {
					sum += s * s
				}
				var rms float32
				if len(chunk) > 0 {
					rms = float32(math.Sqrt(float64(sum / float32(len(chunk)))))
				}
				send(voiceLevelMsg{level: rms})
			}

			// AWAKE: energy-buffer accumulation for short commands (no VAD).
			if currentState == VoiceAwake {
				speechBuf = append(speechBuf, chunk...)
				var sum float32
				for _, s := range chunk {
					sum += s * s
				}
				rms := float32(0)
				if len(chunk) > 0 {
					rms = float32(math.Sqrt(float64(sum / float32(len(chunk)))))
				}
				if rms > speechSilenceThreshold {
					if !speechSpeechSeen {
						dbg("pipeline: awake speech onset")
						send(voiceSpeechStartedMsg{})
						speechSpeechSeen = true
					}
					speechSilenceCount = 0
					speechLastActiveSample = len(speechBuf)
				} else if speechSpeechSeen {
					speechSilenceCount += len(chunk)
				}
				silenceFlush := speechSpeechSeen && speechSilenceCount >= awakeSilenceSamples
				if silenceFlush || len(speechBuf) >= awakeMaxSamples {
					end := speechLastActiveSample + speechTrailSamples
					if end > len(speechBuf) {
						end = len(speechBuf)
					}
					buf := make([]float32, end)
					copy(buf, speechBuf[:end])
					speechBuf = speechBuf[:0]
					speechSpeechSeen = false
					speechSilenceCount = 0
					speechLastActiveSample = 0
					dbg("pipeline: awake flush samples=%d", len(buf))
					text, err := stt.Transcribe(buf)
					if err != nil {
						dbg("pipeline: STT error: %v", err)
						send(voicePipelineErrMsg{fmt.Errorf("STT: %w", err)})
					} else {
						dbg("pipeline: awake transcript=%q", text)
						send(voiceTranscriptMsg{text: text, final: true})
					}
				}
				continue
			}

			// DICTATING / CONVERSING: use VAD for segmentation.
			// Each VAD segment → STT → append to input (not final, not submitted).
			// "over" KWS triggers final submission of whatever is in the input.
			if currentState != VoiceDictating && currentState != VoiceConversing {
				continue
			}
			events := vad.Process(chunk)
			for _, ev := range events {
				if ev.Started {
					dbg("pipeline: speech started state=%s", currentState)
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
					// partial=false means append to input, don't submit yet
					send(voiceTranscriptMsg{text: text, final: false})
				}
			}
		}
	}
}
