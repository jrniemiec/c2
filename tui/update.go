package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jrniemiec/c2/audio"
	"github.com/jrniemiec/c2/c2config"
	"github.com/jrniemiec/c2/internal/clog"
	"github.com/jrniemiec/c2/speech"
	"github.com/jrniemiec/c2/config"
	"github.com/jrniemiec/c2/core"
	"github.com/jrniemiec/c2/engine"
	"github.com/jrniemiec/c2/provider"
)

// Update handles all incoming messages.

func (m *Model) setFocus(pane focusPane) {
	if m.focus == paneInput && pane != paneInput {
		m.input.Blur()
	}
	m.focus = pane
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	// --- window resize ---
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncInputPrompt()
		m.syncLayout()
		m.rebuildConvContent()
		m.rebuildResourceContent()

	// --- window focus/blur ---
	case tea.FocusMsg:
		m.windowFocused = true

	case tea.BlurMsg:
		m.windowFocused = false
		m.cursorVisible = false

	// --- spinner tick ---
	case spinnerTickMsg:
		m.spinnerFrame++
		// Cursor blinks at ~400ms (every 4 ticks of 100ms), only when window and input pane focused.
		if m.spinnerFrame%4 == 0 {
			if m.focus == paneInput && m.windowFocused {
				m.cursorVisible = !m.cursorVisible
			} else {
				m.cursorVisible = false
			}
		}
		if m.streaming || m.isTTSPlaying() || m.transcribing {
			m.rebuildConvContent()
		}
		cmds = append(cmds, spinnerTick())

	// --- streaming token ---
	case streamDeltaMsg:
		m.streamBuf += string(msg)
		// Update the last exchange's in-progress reply.
		if len(m.exchanges) > 0 {
			last := &m.exchanges[len(m.exchanges)-1]
			if !last.complete {
				last.asstMsg.Content = m.streamBuf
			}
		}
		m.rebuildConvContent()
		// Sentence-streaming TTS: detect complete sentences and start playback early.
		if m.ttsAuto && len(m.exchanges) > 0 {
			m.streamSentenceBuf += string(msg)
			sentences, remaining := extractSentences(m.streamSentenceBuf)
			m.streamSentenceBuf = remaining
			exIdx := len(m.exchanges) - 1
			for _, s := range sentences {
				enqueueSentenceTTS(s, ttsStrip(s), exIdx, &m, &cmds)
			}
		}

	// --- streaming done ---
	case streamDoneMsg:
		m.streaming = false
		m.cancelStream = nil
		if msg.err != nil {
			// Show error as a synthetic assistant message
			if len(m.exchanges) > 0 {
				last := &m.exchanges[len(m.exchanges)-1]
				last.asstMsg.Content = fmt.Sprintf("[error: %v]", msg.err)
				last.complete = true
			}
		} else {
			if len(m.exchanges) > 0 {
				last := &m.exchanges[len(m.exchanges)-1]
				last.complete = true
				last.elapsed = msg.result.Elapsed
				last.costUSD = calcExchangeCost(msg.result, m.eng)
				last.model = m.eng.ProfileCode()
			}
			result := msg.result
			m.lastResult = &result
			m.topicStats.Calls++
			m.topicStats.InputTokens += msg.result.Usage.InputTokens
			m.topicStats.OutputTokens += msg.result.Usage.OutputTokens
			m.topicStats.CostUSD += calcExchangeCost(msg.result, m.eng)
			m.sessionStats.Calls++
			m.sessionStats.InputTokens += msg.result.Usage.InputTokens
			m.sessionStats.OutputTokens += msg.result.Usage.OutputTokens
			m.sessionStats.CostUSD += m.topicStats.CostUSD
			// Flush any remaining partial sentence from the stream buffer.
			if m.ttsAuto && len(m.exchanges) > 0 {
				remainder := ttsStrip(strings.TrimSpace(m.streamSentenceBuf))
				m.streamSentenceBuf = ""
				exIdx := len(m.exchanges) - 1
				enqueueSentenceTTS(remainder, remainder, exIdx, &m, &cmds)
			}
		}
		m.streamBuf = ""
		m.streamSentenceBuf = ""
		// If in a voice session with TTS off (or nothing queued), return to
		// CONVERSING immediately. With TTS on, ttsDoneMsg handles the return.
		if m.voiceSession && m.voiceState == VoiceExecuting {
			if !m.ttsAuto && !m.isTTSPlaying() {
				if m.executingTimer != nil {
					m.executingTimer.Stop()
					m.executingTimer = nil
				}
				m.setVoiceState(VoiceConversing)
			}
		}
		m.rebuildConvContent()
		m.input.Focus()

	// --- TTS done ---
	// --- voice pipeline ---
	case voiceLevelMsg:
		if msg.level > m.voicePeakInner {
			m.voicePeakInner = msg.level
		} else {
			m.voicePeakInner *= 0.81
		}
		if msg.level > m.voicePeakOuter {
			m.voicePeakOuter = msg.level
		} else {
			m.voicePeakOuter *= 0.52
		}

	case voicePipelineErrMsg:
		m.voiceErr = msg.err.Error()
		m.voiceReady = false

	case voiceKWSEventMsg:
		cmds = append(cmds, m.handleKWSEvent(msg.keyword)...)

	case voiceAwakeTimeoutMsg:
		if m.voiceState == VoiceAwake {
			clog.Debugf("fsm: AWAKE timeout → %s", m.voiceReturnState)
			m.setVoiceState(m.voiceReturnState)
			go playBeep(beepUnrecognized)
		}

	case voiceExecutingTimeoutMsg:
		if m.voiceState == VoiceExecuting {
			clog.Warnf("fsm: EXECUTING timeout fired — TTS did not start within 5s, forcing IDLE")
			m.setVoiceState(VoiceIdle)
		}

	case clipboardFlashMsg:
		if m.cmdPaneOpen && m.pendingAction == nil && m.lastCmd != nil && m.lastCmd.input == "copy" {
			m.cmdPaneOpen = false
			m.lastCmd = nil
			m.syncLayout()
		}

	case systemReloadMsg:
		data, err := os.ReadFile(m.eng.SystemPath())
		if err != nil && !os.IsNotExist(err) {
			clog.Errorf("system-set: read system.txt: %v", err)
			break
		}
		text := strings.TrimSpace(string(data))
		if err := m.eng.SetSystem(text); err != nil {
			clog.Errorf("system-set: save: %v", err)
		}
		m.rebuildConvContent()

	case resourceReloadMsg:
		// Re-read the file. If the resource viewer is open for this file, refresh it.
		filePath := filepath.Join(m.eng.ResourceDir(), msg.name)
		data, err := os.ReadFile(filePath)
		if err == nil && m.focus == paneResource && m.resourceName == msg.name {
			text := string(data)
			m.resourceLines = strings.Split(text, "\n")
			if m.resourceCursor >= len(m.resourceLines) {
				m.resourceCursor = len(m.resourceLines) - 1
			}
			m.rebuildResourceContent()
			m.scrollResourceToCursor()
		}

	case voiceSpeechStartedMsg:
		// Arm transcribing indicator in any speech-capture state.
		if m.voiceState == VoiceDictating || m.voiceState == VoiceConversing || m.voiceState == VoiceAwake {
			m.transcribing = true
			m.pendingVoiceSubmit = false
			// Clear input in AWAKE only when coming from IDLE — fresh command slate.
			// If we entered AWAKE by interrupting DICTATING/CONVERSING, preserve
			// whatever the user had typed so far.
			if m.voiceState == VoiceAwake && m.voiceReturnState == VoiceIdle {
				m.input.SetValue("")
			}
		}

	case voiceTranscriptMsg:
		m.transcribing = false
		switch m.voiceState {
		case VoiceAwake:
			// Match transcript against command synonym table.
			// Filter Whisper hallucinations like "[BLANK_AUDIO]", "[inaudible]", etc.
			if msg.text == "" || strings.HasPrefix(strings.TrimSpace(msg.text), "[") {
				break
			}
			if label := matchVoiceCommand(msg.text); label != "" {
				clog.Debugf("fsm: STT command match %q → %q", msg.text, label)
				if m.awakeTimer != nil {
					m.awakeTimer.Stop()
					m.awakeTimer = nil
				}
				cmds = append(cmds, m.executeAwakeCommand(label)...)
			} else {
				// No command match — increment fail counter.
				m.voiceFailCount++
				clog.Debugf("fsm: STT no command match for %q, fail=%d", msg.text, m.voiceFailCount)
				m.syncLayout()
				if m.voiceFailCount >= 3 {
					// Third miss — give up, return to previous state.
					if m.awakeTimer != nil {
						m.awakeTimer.Stop()
						m.awakeTimer = nil
					}
					m.voiceFailCount = 0
					m.setVoiceState(m.voiceReturnState)
					go playBeep(beepUnrecognized)
				} else {
					// Stay in AWAKE, reset the timer, prompt the user.
					if m.awakeTimer != nil {
						m.awakeTimer.Stop()
					}
					clog.Debugf("fsm: awake timer reset %s (fail retry)", awakeTimeout)
					m.awakeTimer = time.AfterFunc(awakeTimeout, func() {
						if programSend != nil {
							programSend(voiceAwakeTimeoutMsg{})
						}
					})
					n := m.voiceFailCount
					go sayPrompt(n)
				}
			}
		case VoiceDictating, VoiceConversing:
			if msg.text != "" {
				// Check for clear_input command mid-dictation.
				noteMode := m.pendingDictCmd == "note"
				if label := matchVoiceCommand(msg.text); label == "clear_input" {
					if noteMode {
						m.input.SetValue("// ")
					} else {
						m.input.SetValue("")
					}
					m.input.CursorEnd()
					m.syncLayout()
					go playBeep(beepDictStart)
					break
				}

				// Append segment to whatever is already in the input — the user
				// sees transcription building up. Submission happens on "over".
				existing := strings.TrimSpace(m.input.Value())
				// Strip "//" prefix in note mode before computing combined text.
				if noteMode && strings.HasPrefix(existing, "//") {
					existing = strings.TrimSpace(existing[2:])
				}
				segment := strings.TrimRight(strings.TrimSpace(msg.text), ".!?,;:")

				// Detect "over" at end of segment — submission trigger.
				submitNow := false
				segLower := strings.ToLower(segment)
				if segLower == "over" {
					segment = ""
					submitNow = true
				} else if strings.HasSuffix(segLower, " over") {
					segment = strings.TrimSpace(segment[:len(segment)-len(" over")])
					submitNow = true
				}

				if segment != "" {
					var newVal string
					if existing != "" {
						newVal = existing + " " + segment
					} else {
						newVal = segment
					}
					if noteMode {
						newVal = "// " + newVal
					}
					m.input.SetValue(newVal)
					m.input.CursorEnd()
					m.syncLayout()
				}

				if submitNow {
					if strings.TrimSpace(m.input.Value()) != "" {
						m.pendingVoiceSubmit = true
						cmds = append(cmds, tea.Tick(0, func(time.Time) tea.Msg {
							return voiceAutoSubmitMsg{}
						}))
					}
				}
			}
		}

	case voiceAutoSubmitMsg:
		if !m.pendingVoiceSubmit || m.streaming {
			break
		}
		m.pendingVoiceSubmit = false
		m.transcribing = false
		switch m.voiceState {
		case VoiceDictating:
			go playBeep(beepDictEnd)
			switch m.pendingDictCmd {
			case "note":
				cmds = append(cmds, m.saveVoiceNote())
			default: // "llm" or empty → submit to LLM
				m.setVoiceState(VoiceExecuting)
				cmds = append(cmds, m.sendMessage())
			}
		case VoiceConversing:
			m.setVoiceState(VoiceExecuting)
			cmds = append(cmds, m.sendMessage())
		}

	case ttsDoneMsg:
		if msg.gen != m.ttsGen {
			clog.Debugf("tts: done gen=%d stale (current=%d) — ignored", msg.gen, m.ttsGen)
			break // stale message from a killed process — ignore
		}
		clog.Debugf("tts: done gen=%d pending=%d resQ=%d playQ=%d", msg.gen, len(m.ttsPendingSentences), len(m.resourceTTSQueue), len(m.ttsQueue))
		m.ttsCmd = nil
		m.ttsKokoroStop = nil
		m.ttsExIdx = -1
		// Drain sentence-streaming queue first (higher priority than play-all).
		if len(m.ttsPendingSentences) > 0 {
			next := m.ttsPendingSentences[0]
			m.ttsPendingSentences = m.ttsPendingSentences[1:]
			cmds = append(cmds, startReadoutTTS(next.text, next.exIdx, &m))
		} else if len(m.resourceTTSQueue) > 0 && m.focus == paneResource {
			// Drain resource line-by-line queue.
			next := m.resourceTTSQueue[0]
			m.resourceTTSQueue = m.resourceTTSQueue[1:]
			m.resourceCursor = next
			m.resourceTTSText = m.resourceLines[next]
			m.rebuildResourceContent()
			m.scrollResourceToCursor()
			cmds = append(cmds, startReadoutTTS(ttsStrip(m.resourceTTSText), -1, &m))
		} else if len(m.ttsQueue) > 0 {
			// Drain play-all queue.
			next := m.ttsQueue[0]
			m.ttsQueue = m.ttsQueue[1:]
			if next < len(m.exchanges) {
				enqueueTTSText(ttsText(&m.exchanges[next]), next, &m, &cmds)
			}
		} else if m.correctionTTSPending {
			// Correction speech done — restore voice state now.
			m.correctionTTSPending = false
			if m.correctionVoiceState != VoiceIdle {
				m.setVoiceState(m.correctionVoiceState)
				m.correctionVoiceState = VoiceIdle
			}
		} else if m.voiceState == VoiceExecuting && !m.streaming {
			// All TTS drained — leave EXECUTING.
			if m.executingTimer != nil {
				m.executingTimer.Stop()
				m.executingTimer = nil
			}
			if m.voiceSession {
				m.setVoiceState(VoiceConversing)
			} else {
				m.setVoiceState(m.voiceReturnState)
			}
		}
		// Unmute mic only when all queues are empty (no more TTS coming).
		if !m.isTTSPlaying() && len(m.ttsPendingSentences) == 0 &&
			len(m.ttsQueue) == 0 && len(m.resourceTTSQueue) == 0 {
			setTTSMicMute(false)
		}
		m.rebuildConvContent()

	case shellDoneMsg:
		result := cmdResult{
			input:   "! " + msg.cmd,
			output:  strings.Split(strings.TrimRight(msg.output, "\n"), "\n"),
			isError: msg.exitCode != 0,
		}
		if msg.exitCode != 0 {
			result.warnLine = fmt.Sprintf("[exit %d]", msg.exitCode)
		}
		m.lastCmd = &result
		m.cmdPaneOpen = true
		m.setFocus(paneCmd)
		m.input.Blur()
		m.syncLayout()
		m.cmdScroll.SetContent(renderCmdOutput(&m))
		m.cmdScroll.GotoTop()

	case correctionDoneMsg:
		m.correcting = false
		if msg.err == nil && msg.text != "" {
			// Preserve "//" prefix if original input had it.
			corrected := msg.text
			if strings.HasPrefix(m.input.Value(), "// ") {
				corrected = "// " + corrected
			}
			textChanged := corrected != m.input.Value()
			if textChanged {
				m.correctionFlash = "✓ corrected"
			} else {
				m.correctionFlash = "✓ no changes"
			}
			m.input.SetValue(corrected)
			m.input.CursorEnd()
			if m.speakCorrectedNote && textChanged {
				// Speak result; restore voice state only after TTS finishes.
				m.correctionTTSPending = true
				cmds = append(cmds, startCommandTTS("After correction: "+msg.text, &m))
			} else {
				// Restore voice state immediately.
				if m.correctionVoiceState != VoiceIdle {
					m.setVoiceState(m.correctionVoiceState)
					m.correctionVoiceState = VoiceIdle
				}
			}
		} else {
			// Error — always restore immediately.
			if m.correctionVoiceState != VoiceIdle {
				m.setVoiceState(m.correctionVoiceState)
				m.correctionVoiceState = VoiceIdle
			}
			if msg.err != nil {
				clog.Errorf("correction error: %v", msg.err)
				errStr := msg.err.Error()
				if len(errStr) > 40 {
					errStr = errStr[:40] + "…"
				}
				m.correctionFlash = "✗ " + errStr
			}
		}
		cmds = append(cmds, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return correctionFlashMsg{}
		}))
		m.syncLayout()

	case correctionFlashMsg:
		m.correctionFlash = ""

	// --- mouse ---
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.userScrolled = true
			m.conv.ScrollUp(3)
		case tea.MouseButtonWheelDown:
			m.conv.ScrollDown(3)
			if m.conv.AtBottom() {
				m.userScrolled = false
			}
		}

	// --- keyboard ---
	case tea.KeyMsg:
		// Resource overlay: handle all keys independently and return early.
		if m.focus == paneResource {
			cmds = append(cmds, m.handleResourceKey(msg)...)
			return m, tea.Batch(cmds...)
		}
		// Topic picker overlay: handle all keys independently and return early.
		if m.focus == paneTopicPicker {
			cmds = append(cmds, m.handleTopicPickerKey(msg)...)
			return m, tea.Batch(cmds...)
		}
		// Profile picker overlay: handle all keys independently and return early.
		if m.focus == paneProfilePicker {
			cmds = append(cmds, m.handleProfilePickerKey(msg)...)
			return m, tea.Batch(cmds...)
		}

		// Ctrl+Space: keyboard wake word toggle (voice mode only).
		if m.mode == modeVoice && key.Matches(msg, keys.WakeWord) {
			switch m.voiceState {
			case VoiceAwake:
				// Already awake — cancel back to previous state.
				clog.Debugf("fsm: ctrl+space cancel AWAKE → %s", m.voiceReturnState)
				if m.awakeTimer != nil {
					m.awakeTimer.Stop()
					m.awakeTimer = nil
				}
				m.setVoiceState(m.voiceReturnState)
				go playBeep(beepUnrecognized)
			default:
				// Activate AWAKE — same as "computer" KWS event.
				clog.Debugf("fsm: ctrl+space → AWAKE")
				m.suspendTTS()
				m.voiceReturnState = m.voiceState
				m.voiceFailCount = 0
				m.setVoiceState(VoiceAwake)
				clog.Debugf("fsm: awake timer set 5s (ctrl+space)")
				m.awakeTimer = time.AfterFunc(5*time.Second, func() {
					if programSend != nil {
						programSend(voiceAwakeTimeoutMsg{})
					}
				})
				go playBeep(beepWakeAck)
			}
			return m, tea.Batch(cmds...)
		}

		// Bracketed paste: entire pasted content arrives as one KeyMsg with Paste=true.
		if msg.Paste && m.focus == paneInput && !m.streaming && m.pendingAction == nil {
			// Normalize \r\n and bare \r to \n (terminals paste with \r endings).
			content := strings.ReplaceAll(string(msg.Runes), "\r\n", "\n")
			content = strings.ReplaceAll(content, "\r", "\n")
			lines := strings.Split(content, "\n")
			if len(lines) > 20 || len([]rune(content)) > 256 {
				pre := m.input.Value()
				blob := pre
				if blob != "" && !strings.HasSuffix(blob, "\n") {
					blob += "\n"
				}
				blob += content
				m.pastedBlob = blob
				lineCount := strings.Count(content, "\n") + 1
				kb := float64(len(content)) / 1024.0
				label := fmt.Sprintf("[pasted: %d lines · %.1f KB]", lineCount, kb)
				// Keep pre-text visible; append label so user can see what was typed.
				m.input.SetValue(pre + label)
				m.input.CursorEnd()
				m.completionItems = nil
				m.completionIdx = -1
				m.syncLayout()
			} else {
				m.input.InsertString(content)
			}
			break
		}

		// Tab with no active completion → toggle focus between input and conv pane.
		if msg.Type == tea.KeyTab && len(m.completionItems) == 0 && len(m.paramItems) == 0 {
			if m.focus == paneInput {
				m.setFocus(paneConv)
				m.input.Blur()
				if m.focusedExIdx < 0 && len(m.exchanges) > 0 {
					m.focusedExIdx = len(m.exchanges) - 1
				}
				m.rebuildConvContent()
			} else if m.focus == paneConv {
				m.setFocus(paneInput)
				m.focusedExIdx = -1
				m.input.Focus()
				m.rebuildConvContent()
			}
			return m, tea.Batch(cmds...)
		}

		// [ / ] — adjust TTS speed while playing.
		// For say: restart current sentence immediately at new rate.
		// For kokoro: update rate only — takes effect on next sentence (inference cannot be interrupted).
		if msg.Type == tea.KeyRunes && m.isTTSPlaying() {
			switch string(msg.Runes) {
			case "[":
				if m.ttsReadoutRate > 80 {
					m.ttsReadoutRate -= 20
				}
				if m.c2cfg.TTSReadoutBackend != "kokoro" {
					text := m.ttsCurrentText
					exIdx := m.ttsExIdx
					m.killTTS()
					cmds = append(cmds, startReadoutTTS(text, exIdx, &m))
					return m, tea.Batch(cmds...)
				}
			case "]":
				if m.ttsReadoutRate < 500 {
					m.ttsReadoutRate += 20
				}
				if m.c2cfg.TTSReadoutBackend != "kokoro" {
					text := m.ttsCurrentText
					exIdx := m.ttsExIdx
					m.killTTS()
					cmds = append(cmds, startReadoutTTS(text, exIdx, &m))
					return m, tea.Batch(cmds...)
				}
			}
		}

		// Any key dismisses the cmd pane (unless a pending action requires confirmation).
		if m.cmdPaneOpen && m.pendingAction == nil && m.focus == paneInput {
			m.cmdPaneOpen = false
			m.lastCmd = nil
			m.syncLayout()
		}

		completionHandled := false
		switch {

		// Ctrl+C: stop TTS, cancel stream, or quit
		case key.Matches(msg, keys.Cancel):
			if m.isTTSPlaying() {
				m.killTTS()
				m.rebuildConvContent()
				return m, nil
			}
			if m.streaming && m.cancelStream != nil {
				m.cancelStream()
				return m, nil
			}
			now := time.Now()
			if now.Sub(m.lastCtrlC) < 500*time.Millisecond {
				m.killTTS()
				return m, tea.Quit
			}
			m.lastCtrlC = now
			return m, nil

		// Esc: exit completion, cancel pending action, collapse cmd pane, or return focus to input
		case key.Matches(msg, keys.Dismiss):
			if len(m.paramItems) > 0 {
				m.paramItems = nil
				m.paramIdx = -1
				m.syncLayout()
			} else if len(m.completionItems) > 0 {
				m.completionItems = nil
				m.completionIdx = -1
				m.syncLayout()
			} else {
				// Single Esc: cancel any pending action, close cmd pane,
				// clear input, and return focus to input — all in one gesture.
				if m.pendingAction != nil {
					m.pendingAction = nil
					m.pendingPost = nil
					m.confirmBuf = ""
					m.focusedExIdx = -1
					m.deletingExIdx = -1
					canceled := cmdResult{input: m.lastCmd.input, output: []string{"operation canceled"}}
					m.lastCmd = &canceled
					m.cmdScroll.SetContent(renderCmdOutput(&m))
					m.cmdScroll.GotoTop()
				}
				m.cmdPaneOpen = false
				m.lastCmd = nil
				m.input.Reset()
				m.input.SetHeight(1)
				m.pastedBlob = ""
				m.historyIdx = -1
				m.historySaved = ""
				m.focusedExIdx = -1
				m.killTTS()
				m.setFocus(paneInput)
				m.input.Focus()
				m.rebuildConvContent()
				m.syncLayout()
			}

		// [DEV] Ctrl+Y: toggle transcribing state for UI testing
		case key.Matches(msg, keys.DEVToggleTranscribing):
			m.transcribing = !m.transcribing

		// Tab: fill selected completion into input, or switch voice/text mode
		case key.Matches(msg, keys.FillCompletion):
			if len(m.paramItems) > 0 && m.paramIdx >= 0 {
				// Fill selected parameter value into the input, replacing any partial arg.
				val := m.input.Value()
				cmd := val
				if idx := strings.Index(val, " "); idx >= 0 {
					cmd = val[:idx]
				}
				m.input.SetValue(cmd + " " + m.paramItems[m.paramIdx])
				m.input.CursorEnd()
				m.paramItems = nil
				m.paramIdx = -1
				completionHandled = true
				m.syncLayout()
			} else if len(m.completionItems) > 0 && m.completionIdx >= 0 {
				filled := m.completionItems[m.completionIdx].cmd + " "
				m.input.SetValue(filled)
				m.input.CursorEnd()
				m.completionItems = nil
				m.completionIdx = -1
				// Immediately show param picker if this command supports it.
				items := contextualParams(&m, strings.TrimSpace(filled))
				m.paramItems = items
				if len(items) > 0 {
					m.paramIdx = 0
				} else {
					m.paramIdx = -1
				}
				completionHandled = true
				m.syncLayout()
			}

		// Enter: execute completion, confirm pending action, send (input pane), or dismiss (conv pane)
		case key.Matches(msg, keys.Send):
			if len(m.paramItems) > 0 && m.paramIdx >= 0 {
				// Fill selected parameter value into the input — don't execute yet.
				val := m.input.Value()
				cmd := val
				if idx := strings.Index(val, " "); idx >= 0 {
					cmd = val[:idx]
				}
				m.input.SetValue(cmd + " " + m.paramItems[m.paramIdx])
				m.input.CursorEnd()
				m.paramItems = nil
				m.paramIdx = -1
				m.input.Focus()
				m.syncLayout()
			} else if len(m.completionItems) > 0 && m.completionIdx >= 0 {
				selected := m.completionItems[m.completionIdx].cmd
				m.completionItems = nil
				m.completionIdx = -1
				m.input.SetValue("")
				m.syncLayout()
				val := strings.TrimSpace(selected)
				if strings.HasPrefix(val, "/") {
					result := handleCommand(&m, val)
					if result.quit {
						m.killTTS()
						return m, tea.Quit
					}
					if result.execCmd != nil {
						cmds = append(cmds, result.execCmd)
					} else if result.suppressCmdPane {
						m.syncLayout()
						m.rebuildResourceContent()
					} else {
						m.lastCmd = &result
						m.cmdPaneOpen = true
						if result.isError {
							m.setFocus(paneInput)
							m.input.Focus()
						} else {
							m.setFocus(paneCmd)
							m.input.Blur()
						}
						m.rebuildConvContent()
						m.cmdScroll.SetContent(renderCmdOutput(&m))
						m.cmdScroll.GotoTop()
						m.syncLayout()
					}
				}
			} else if m.focus == paneCmd && m.pendingAction == nil {
				m.cmdPaneOpen = false
				m.lastCmd = nil
				m.setFocus(paneInput)
				m.input.Focus()
				m.syncLayout()
			} else if m.pendingAction != nil {
				if strings.ToLower(strings.TrimSpace(m.confirmBuf)) == "yes" {
					result := m.pendingAction()
					m.pendingAction = nil
					m.confirmBuf = ""
					if m.pendingPost != nil {
						m.pendingPost(&m)
						m.pendingPost = nil
					}
					m.focusedExIdx = -1
					m.lastCmd = &result
					m.rebuildConvContent()
					m.cmdScroll.SetContent(renderCmdOutput(&m))
					m.cmdScroll.GotoTop()
				} else {
					m.pendingAction = nil
					m.confirmBuf = ""
					m.focusedExIdx = -1
					m.deletingExIdx = -1
					canceled := cmdResult{input: m.lastCmd.input, output: []string{"operation canceled"}}
					m.lastCmd = &canceled
					m.cmdScroll.SetContent(renderCmdOutput(&m))
					m.cmdScroll.GotoTop()
				}
				m.setFocus(paneInput)
				m.input.Focus()
				m.rebuildConvContent()
				m.syncLayout()
			} else if m.focus == paneConv {
				m.setFocus(paneInput)
				m.focusedExIdx = -1
				m.input.Focus()
				m.rebuildConvContent()
			} else if !m.streaming {
				val := strings.TrimSpace(m.input.Value())
				if val == "" {
					m.scrollToBottom()
				} else if strings.HasPrefix(val, "//") {
					// Personal note — save to history, never sent to LLM.
					// If a paste blob is pending, strip the // prefix from the blob
					// (blob already contains the pre-text including "// ...").
					var text string
					if m.pastedBlob != "" {
						// blob starts with the pre-text, e.g. "// intro\npasted content..."
						raw := m.pastedBlob
						m.pastedBlob = ""
						if strings.HasPrefix(raw, "//") {
							raw = strings.TrimSpace(raw[2:])
						}
						text = raw
					} else {
						text = strings.TrimSpace(val[2:])
					}
					m.pushHistory(val)
					m.input.Reset()
					if text != "" {
						if err := m.eng.AddNote(text); err == nil {
							m.exchanges = append(m.exchanges, exchange{
								userMsg:  core.Message{Role: core.RoleNote, Content: text, Time: time.Now()},
								isNote:   true,
								complete: true,
								expanded: true,
							})
							m.rebuildConvContent()
						}
					}
				} else {
					m.pushHistory(val)
					if strings.HasPrefix(val, "!") {
						// Shell command — run via sh -c, show output in cmd pane.
						m.input.Reset()
						shellCmd := strings.TrimSpace(val[1:])
						cmds = append(cmds, runShellCmd(shellCmd))
						break
					}
					if !strings.HasPrefix(val, "/") && looksLikeCommand(val) {
						val = "/" + val
					}
					if strings.HasPrefix(val, "/") {
						result := handleCommand(&m, val)
						if result.quit {
							m.killTTS()
							return m, tea.Quit
						}
						m.input.Reset()
						if result.execCmd != nil {
							cmds = append(cmds, result.execCmd)
						} else if result.suppressCmdPane {
							m.syncLayout()
							m.rebuildResourceContent()
						} else {
							m.lastCmd = &result
							m.cmdPaneOpen = true
							if result.isError {
								m.setFocus(paneInput)
								m.input.Focus()
							} else {
								m.setFocus(paneCmd)
								m.input.Blur()
							}
							m.syncLayout()
							m.cmdScroll.SetContent(renderCmdOutput(&m))
							m.cmdScroll.GotoTop()
							// If /play-all queued entries, kick off playback now.
							if !m.isTTSPlaying() && len(m.ttsQueue) > 0 {
								next := m.ttsQueue[0]
								m.ttsQueue = m.ttsQueue[1:]
								if next < len(m.exchanges) {
									enqueueTTSText(ttsText(&m.exchanges[next]), next, &m, &cmds)
								}
							}
						}
					} else {
						cmds = append(cmds, m.sendMessage())
					}
				}
			}

		// Shift+Enter: newline in input
		case key.Matches(msg, keys.Newline):
			if m.focus == paneInput {
				m.input.InsertString("\n")
			}

		// Arrow up/down: completion, history in input pane, scroll cmd pane, navigate conv pane, or scroll conv
		case key.Matches(msg, keys.NavUp):
			if len(m.paramItems) > 0 {
				if m.paramIdx > 0 {
					m.paramIdx--
				}
			} else if len(m.completionItems) > 0 {
				if m.completionIdx > 0 {
					m.completionIdx--
				}
			} else if m.focus == paneInput && m.input.Line() > 0 {
				// Multi-line input: cursor is not on the first line — move up within textarea.
				var inputCmd tea.Cmd
				m.input, inputCmd = m.input.Update(msg)
				cmds = append(cmds, inputCmd)
			} else if m.focus == paneInput && m.historyIdx != -1 {
				// Already browsing — continue going back.
				if m.historyIdx > 0 {
					m.historyIdx--
				}
				m.input.SetValue(m.inputHistory[m.historyIdx])
				m.input.CursorEnd()
			} else if m.focus == paneInput && m.input.Value() == "" && len(m.inputHistory) > 0 {
				// Start history browsing from an empty input.
				m.historySaved = ""
				m.historyIdx = len(m.inputHistory) - 1
				m.input.SetValue(m.inputHistory[m.historyIdx])
				m.input.CursorEnd()
			} else if m.focus == paneCmd {
				m.cmdScroll.ScrollUp(3)
			} else if m.focus == paneConv {
				prev := m.focusedExIdx
				if m.focusedExIdx < 0 {
					m.focusedExIdx = len(m.exchanges) - 1
					m.rebuildConvContent()
				} else if m.focusedExIdx > 0 {
					m.focusedExIdx--
					m.rebuildConvContent()
				} else {
					// Already at first exchange — scroll up within it.
					m.conv.ScrollUp(3)
				}
				_ = prev
			} else {
				m.userScrolled = true
				m.conv.ScrollUp(3)
			}

		case key.Matches(msg, keys.NavDown):
			if len(m.paramItems) > 0 {
				if m.paramIdx < len(m.paramItems)-1 {
					m.paramIdx++
				}
			} else if len(m.completionItems) > 0 {
				if m.completionIdx < len(m.completionItems)-1 {
					m.completionIdx++
				}
			} else if m.focus == paneInput && m.input.Line() < strings.Count(m.input.Value(), "\n") {
				// Multi-line input: cursor is not on the last line — move down within textarea.
				var inputCmd tea.Cmd
				m.input, inputCmd = m.input.Update(msg)
				cmds = append(cmds, inputCmd)
			} else if m.focus == paneInput && m.historyIdx != -1 {
				if m.historyIdx < len(m.inputHistory)-1 {
					m.historyIdx++
					m.input.SetValue(m.inputHistory[m.historyIdx])
					m.input.CursorEnd()
				} else {
					// Past the newest: restore draft and exit history mode.
					m.input.SetValue(m.historySaved)
					m.input.CursorEnd()
					m.historyIdx = -1
					m.historySaved = ""
				}
			} else if m.focus == paneCmd {
				m.cmdScroll.ScrollDown(3)
			} else if m.focus == paneConv {
				if m.focusedExIdx >= 0 && m.focusedExIdx < len(m.exchanges)-1 {
					m.focusedExIdx++
					m.rebuildConvContent()
				} else {
					// Already at last exchange — scroll down within it.
					m.conv.ScrollDown(3)
				}
			} else {
				m.conv.ScrollDown(3)
				if m.conv.AtBottom() {
					m.userScrolled = false
				}
			}

		// Page Up/Down: scroll cmd pane or conversation viewport
		case key.Matches(msg, keys.ScrollUp):
			if m.focus == paneCmd {
				m.cmdScroll.HalfPageUp()
			} else {
				m.userScrolled = true
				m.conv.HalfPageUp()
			}

		case key.Matches(msg, keys.ScrollDown):
			if m.focus == paneCmd {
				m.cmdScroll.HalfPageDown()
			} else {
				m.conv.HalfPageDown()
				if m.conv.AtBottom() {
					m.userScrolled = false
				}
			}

		// Ctrl+S: copy input or focused exchange to clipboard
		case key.Matches(msg, keys.CopyToClipboard):
			var text string
			if m.focus == paneConv && m.focusedExIdx >= 0 {
				ex := m.exchanges[m.focusedExIdx]
				text = ex.userMsg.Content
				if ex.asstMsg.Content != "" {
					text += "\n\n" + ex.asstMsg.Content
				}
			} else {
				text = m.input.Value()
				if m.pendingDictCmd == "note" {
					text = strings.TrimSpace(strings.TrimPrefix(text, "//"))
				}
			}
			if text != "" {
				cmd := exec.Command("pbcopy")
				cmd.Stdin = strings.NewReader(text)
				_ = cmd.Run()
				m.lastCmd = &cmdResult{input: "copy", output: []string{"copied to clipboard"}}
				m.cmdPaneOpen = true
				m.cmdScroll.SetContent(renderCmdOutput(&m))
				m.cmdScroll.GotoTop()
				m.syncLayout()
				cmds = append(cmds, tea.Tick(1500*time.Millisecond, func(t time.Time) tea.Msg {
					return clipboardFlashMsg{}
				}))
			}
			return m, tea.Batch(cmds...)

		// Ctrl+L: clear screen
		case key.Matches(msg, keys.ClearScreen):
			return m, tea.ClearScreen

		// Ctrl+N: toggle focus between input and conversation pane
		case key.Matches(msg, keys.FocusConv):
			if m.focus == paneInput {
				m.setFocus(paneConv)
				m.input.Blur()
				if m.focusedExIdx < 0 && len(m.exchanges) > 0 {
					m.focusedExIdx = len(m.exchanges) - 1
				}
				m.rebuildConvContent()
			} else if m.focus == paneConv {
				m.setFocus(paneInput)
				m.focusedExIdx = -1
				m.input.Focus()
				m.rebuildConvContent()
			}

		case key.Matches(msg, keys.SwitchTopic):
			m.openTopicPicker()
		case key.Matches(msg, keys.SwitchProfile):
			m.openProfilePicker()

		case key.Matches(msg, keys.CorrectInput):
			if !m.correcting && strings.TrimSpace(m.input.Value()) != "" {
				m.correcting = true
				// Strip leading "//" note prefix before correcting.
				text := m.input.Value()
				if strings.HasPrefix(text, "//") {
					text = strings.TrimSpace(text[2:])
					m.input.SetValue("// " + text)
				}
				// Pause STT for the duration of the correction call.
				m.correctionVoiceState = m.voiceState
				if m.voiceState != VoiceIdle {
					m.setVoiceState(VoiceIdle)
				}
				cmds = append(cmds, doCorrection(text, m.cfg, m.c2cfg))
			}

		default:
			// v/d/s: nav mode actions — only when paneConv is focused.
			if m.focus == paneConv && m.focusedExIdx >= 0 && msg.Type == tea.KeyRunes {
				switch string(msg.Runes) {
				case "s":
					if m.isTTSPlaying() {
						// Already playing — stop it and clear queue.
						m.killTTS()
						m.rebuildConvContent()
					} else {
						exIdx := m.focusedExIdx
						enqueueTTSText(ttsText(&m.exchanges[exIdx]), exIdx, &m, &cmds)
					}
					return m, tea.Batch(cmds...)
				case "v":
					ex := &m.exchanges[m.focusedExIdx]
					if m.isLongEntry(*ex) {
						ex.expanded = !ex.expanded
						m.rebuildConvContent()
					}
					return m, tea.Batch(cmds...)
				case "x":
					exIdx := m.focusedExIdx
					doDelete := func() {
						if err := m.eng.DeleteAt(exIdx); err != nil {
							m.lastCmd = &cmdResult{input: fmt.Sprintf("delete entry #%d", exIdx+1), output: []string{err.Error()}, isError: true}
							m.cmdPaneOpen = true
						} else {
							m.exchanges = append(m.exchanges[:exIdx], m.exchanges[exIdx+1:]...)
							if m.focusedExIdx >= len(m.exchanges) {
								m.focusedExIdx = len(m.exchanges) - 1
							}
							if len(m.exchanges) == 0 {
								m.focus = paneInput
								m.focusedExIdx = -1
								m.input.Focus()
							}
							m.rebuildConvContent()
							m.syncLayout()
						}
					}
					if m.ackAllDeletions {
						label := fmt.Sprintf("delete entry #%d", exIdx+1)
						m.deletingExIdx = exIdx
						m.pendingAction = func() cmdResult {
							doDelete()
							m.deletingExIdx = -1
							return okResult(label, []string{fmt.Sprintf("entry #%d deleted", exIdx+1)})
						}
						m.lastCmd = &cmdResult{
							input:  label,
							output: []string{fmt.Sprintf("Entry #%d will be permanently deleted.", exIdx+1), "[yes] to confirm, other input or Esc to cancel:"},
						}
						m.cmdPaneOpen = true
						m.cmdScroll.SetContent(renderCmdOutput(&m))
						m.cmdScroll.GotoTop()
						m.setFocus(paneInput)
						m.input.Focus()
						m.syncLayout()
					} else {
						doDelete()
					}
					return m, tea.Batch(cmds...)
				}
			}
			// Any edit while browsing history exits history mode, keeping current entry.
			if m.focus == paneInput && m.historyIdx != -1 {
				m.historyIdx = -1
				m.historySaved = ""
			}
			if !completionHandled && m.focus == paneInput && !m.streaming && m.pendingAction == nil {
				var inputCmd tea.Cmd
				m.input, inputCmd = m.input.Update(msg)
				visualH := m.inputVisualHeight()
				if visualH != m.input.Height() {
					m.input.SetHeight(visualH)
				}
				cmds = append(cmds, inputCmd)
				// Auto-insert space after '!' so the command reads "! cmd" not "!cmd".
				if m.input.Value() == "!" {
					m.input.SetValue("! ")
					m.input.CursorEnd()
				}
				// Update completion list based on new input value.
				val := m.input.Value()
				if strings.HasPrefix(val, "/") && !strings.Contains(val, " ") {
					items := filterCompletions(val)
					if len(items) == 1 && items[0].cmd == val {
						// Exact match — hide completion list, show param picker immediately.
						items = nil
						params := contextualParams(&m, val)
						m.paramItems = params
						if len(params) > 0 {
							m.paramIdx = 0
						} else {
							m.paramIdx = -1
						}
					} else {
						m.paramItems = nil
						m.paramIdx = -1
					}
					m.completionItems = items
					if len(items) > 0 {
						m.completionIdx = 0 // pre-highlight first match
					} else {
						m.completionIdx = -1
					}
				} else if strings.HasPrefix(val, "/") && strings.Contains(val, " ") {
					// "/cmd " or "/cmd <partial>" — show param picker, filtered by partial arg.
					fields := strings.SplitN(val, " ", 2)
					cmd := strings.ToLower(fields[0])
					partial := ""
					if len(fields) == 2 {
						partial = strings.ToLower(strings.TrimLeft(fields[1], " "))
					}
					allItems := contextualParams(&m, cmd)
					var items []string
					for _, item := range allItems {
						if partial == "" || strings.HasPrefix(strings.ToLower(item), partial) {
							items = append(items, item)
						}
					}
					m.paramItems = items
					if len(items) > 0 {
						m.paramIdx = 0
					} else {
						m.paramIdx = -1
					}
					m.completionItems = nil
					m.completionIdx = -1
				} else {
					m.completionItems = nil
					m.completionIdx = -1
					m.paramItems = nil
					m.paramIdx = -1
				}
				m.syncLayout()
				m.cursorVisible = true
			}
			if m.pendingAction != nil {
				switch msg.Type {
				case tea.KeyRunes:
					m.confirmBuf += string(msg.Runes)
					m.cmdScroll.SetContent(renderCmdOutput(&m))
				case tea.KeyBackspace:
					if len(m.confirmBuf) > 0 {
						m.confirmBuf = m.confirmBuf[:len(m.confirmBuf)-1]
						m.cmdScroll.SetContent(renderCmdOutput(&m))
					}
				}
			}
		}
	}

	return m, tea.Batch(cmds...)
}

// sendMessage takes the current input, sends it to the engine, and returns a Cmd.
func (m *Model) sendMessage() tea.Cmd {
	var rawPrompt string
	isPasted := m.pastedBlob != ""
	if isPasted {
		rawPrompt = m.pastedBlob
		m.pastedBlob = ""
	} else {
		rawPrompt = strings.TrimSpace(m.input.Value())
	}
	if rawPrompt == "" {
		return nil
	}

	// Resolve @ref file injections. Abort on error, show in cmd pane.
	prompt, err := core.ResolveAtRefs(rawPrompt, m.eng.ResourceDir())
	if err != nil {
		m.pastedBlob = "" // clear any pending blob
		errRes := cmdResult{input: rawPrompt, output: []string{err.Error()}, isError: true}
		m.lastCmd = &errRes
		m.cmdPaneOpen = true
		m.setFocus(paneInput)
		m.input.Focus()
		m.syncLayout()
		m.cmdScroll.SetContent(renderCmdOutput(m))
		m.cmdScroll.GotoTop()
		return nil
	}

	m.input.Reset()
	m.input.SetHeight(1)
	m.input.Blur()
	m.streamSentenceBuf = ""
	m.ttsPendingSentences = nil

	m.exchanges = append(m.exchanges, exchange{
		userMsg: core.Message{
			Role:    core.RoleUser,
			Content: prompt,
		},
		complete: false,
		isPasted: isPasted,
		expanded: true,
	})
	m.streaming = true
	m.streamBuf = ""
	m.userScrolled = false
	m.rebuildConvContent()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelStream = cancel

	eng := m.eng
	// Bubbletea Cmds run in a goroutine and return exactly one tea.Msg.
	// For streaming we use tea.Sequence: each delta token is sent as a
	// streamDeltaMsg, and the final result as streamDoneMsg. We implement
	// this by sending deltas via a channel that is drained by a Cmd chain,
	// but the simplest correct approach with Bubbletea is to use
	// tea.Program.Send from the goroutine. We access it via a closure
	// populated by Start().
	return func() tea.Msg {
		opts := engine.ChatOptions{}
		result, err := eng.Chat(ctx, prompt, opts, func(delta string) error {
			if programSend != nil {
				programSend(streamDeltaMsg(delta))
			}
			return nil
		})
		return streamDoneMsg{result: result, err: err}
	}
}

// =============================================================================
// Shell command execution (! prefix)
// =============================================================================

type shellDoneMsg struct {
	cmd      string
	output   string
	exitCode int
}

// runShellCmd executes cmd via the user's login shell with a 30s timeout and returns shellDoneMsg.
func runShellCmd(cmd string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "sh"
		}
		c := exec.CommandContext(ctx, shell, "-i", "-c", cmd)
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		out, err := c.CombinedOutput()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				// Non-exit error (e.g. timeout): treat as exit 1.
				exitCode = 1
				out = append(out, []byte("\n"+err.Error())...)
			}
		}
		return shellDoneMsg{cmd: cmd, output: string(out), exitCode: exitCode}
	}
}

// =============================================================================
// Input correction (Ctrl+R)
// =============================================================================

// defaultCorrectionPrompt is used when correction_prompt is not set in config.
// Edit this constant or set "correction_prompt" in the c2 config section to customise.
const defaultCorrectionPrompt = "Correct the spelling and grammar of the following text. " +
	"Return only the corrected text with no explanations, no quotes, and no additional commentary."

// doCorrection sends the input text to the correction profile for spell/grammar fixing.
// The result is returned as a correctionDoneMsg.
func doCorrection(text string, cfg config.Config, c2cfg c2config.C2Config) tea.Cmd {
	return func() tea.Msg {
		// Resolve which profile to use.
		profileCode := c2cfg.CorrectionProfile
		if profileCode == "" {
			profileCode = "oai-mini"
		}
		prof, ok := cfg.Profiles[profileCode]
		if !ok && profileCode != cfg.DefaultProfile {
			// Fall back to the default profile before giving up.
			profileCode = cfg.DefaultProfile
			prof, ok = cfg.Profiles[profileCode]
		}
		if !ok {
			return correctionDoneMsg{err: fmt.Errorf("correction profile %q not found in config", profileCode)}
		}

		// Resolve system prompt.
		systemPrompt := c2cfg.CorrectionPrompt
		if systemPrompt == "" {
			systemPrompt = defaultCorrectionPrompt
		}

		prov, err := provider.New(prof)
		if err != nil {
			return correctionDoneMsg{err: fmt.Errorf("correction: init provider: %w", err)}
		}

		msgs := []core.Message{
			{Role: core.RoleUser, Content: text},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		response, _, err := prov.Chat(ctx, systemPrompt, msgs)
		if err != nil {
			return correctionDoneMsg{err: fmt.Errorf("correction: %w", err)}
		}
		return correctionDoneMsg{text: strings.TrimSpace(response)}
	}
}

// ttsText returns the assistant reply as plain text to speak.
// Notes speak the note content; regular exchanges speak only the assistant reply.
func ttsText(ex *exchange) string {
	if ex.isNote {
		return ttsStrip(ex.userMsg.Content)
	}
	return ttsStrip(ex.asstMsg.Content)
}

func ttsStrip(s string) string { return core.TTSStrip(s) }

// extractSentences splits s into complete sentences and a leftover remainder.
// A sentence boundary is .!? followed by whitespace or end of string, with
// two exceptions to avoid false splits:
//   - '.' preceded by a digit (decimal: "3.14")
//   - '.' preceded by a single uppercase letter (abbreviation: "Dr.", "U.S.")
//
// Sentences shorter than minSentenceLen runes are merged into the remainder.
const minSentenceLen = 20

func extractSentences(s string) (sentences []string, remainder string) {
	runes := []rune(s)
	n := len(runes)
	start := 0

	for i := 0; i < n; i++ {
		ch := runes[i]
		if ch != '.' && ch != '!' && ch != '?' {
			continue
		}
		// For '.' check abbreviation/decimal exceptions.
		if ch == '.' && i > 0 {
			prev := runes[i-1]
			if prev >= '0' && prev <= '9' {
				continue // decimal number
			}
			if prev >= 'A' && prev <= 'Z' && (i < 2 || runes[i-2] == ' ' || runes[i-2] == '.') {
				continue // single-letter abbreviation
			}
		}
		// Consume trailing punctuation (e.g. "?!", "...")
		end := i
		for end+1 < n && (runes[end+1] == '.' || runes[end+1] == '!' || runes[end+1] == '?') {
			end++
		}
		// Must be followed by whitespace or end of string.
		if end+1 < n && runes[end+1] != ' ' && runes[end+1] != '\n' && runes[end+1] != '\t' {
			i = end
			continue
		}
		candidate := strings.TrimSpace(string(runes[start : end+1]))
		if len([]rune(candidate)) >= minSentenceLen {
			sentences = append(sentences, candidate)
			// Skip leading whitespace after the boundary.
			i = end + 1
			for i < n && (runes[i] == ' ' || runes[i] == '\n' || runes[i] == '\t') {
				i++
			}
			start = i
			i-- // loop will i++
		}
	}
	remainder = string(runes[start:])
	return
}

// enqueueTTSText splits full exchange text into sentences and enqueues each one.
// Use this instead of startTTS when playing back a full exchange (s key, replay, play-all).
func enqueueTTSText(text string, exIdx int, m *Model, cmds *[]tea.Cmd) {
	sentences, remainder := extractSentences(ttsStrip(text))
	remainder = strings.TrimSpace(ttsStrip(remainder))
	if remainder != "" {
		sentences = append(sentences, remainder)
	}
	for _, s := range sentences {
		enqueueSentenceTTS(s, s, exIdx, m, cmds)
	}
}

// enqueueSentenceTTS appends a sentence to the TTS pipeline.
// If nothing is playing it starts immediately; otherwise it queues.
func enqueueSentenceTTS(text, stripped string, exIdx int, m *Model, cmds *[]tea.Cmd) {
	if stripped == "" {
		return
	}
	if !m.isTTSPlaying() && len(m.ttsPendingSentences) == 0 {
		*cmds = append(*cmds, startReadoutTTS(stripped, exIdx, m))
	} else {
		m.ttsPendingSentences = append(m.ttsPendingSentences, ttsPendingItem{text: stripped, exIdx: exIdx})
	}
}

// startReadoutTTS launches the readout TTS backend (Kokoro or say) for exchange
// text spoken on demand (s key, replay, play-all, resource view).
func startReadoutTTS(text string, exIdx int, m *Model) tea.Cmd {
	// TTS has started — cancel the executing safety timer so it doesn't
	// fire mid-playback and incorrectly reset state.
	if m.executingTimer != nil {
		m.executingTimer.Stop()
		m.executingTimer = nil
	}
	m.ttsGen++
	gen := m.ttsGen
	m.ttsExIdx = exIdx
	m.ttsCurrentText = text
	m.rebuildConvContent()
	setTTSMicMute(true)
	preview := text
	if len([]rune(preview)) > 60 {
		preview = string([]rune(preview)[:60]) + "…"
	}
	clog.Debugf("tts: readout gen=%d backend=%s exIdx=%d text=%q", gen, m.c2cfg.TTSReadoutBackend, exIdx, preview)

	if m.c2cfg.TTSReadoutBackend == "kokoro" {
		return startTTSKokoroStream(text, gen, m)
	}
	return startTTSSay(text, m.ttsReadoutRate, m.c2cfg.TTSReadoutVoice, gen, m)
}

// startCommandTTS speaks a short command acknowledgement or response via say(1).
// It kills any running Kokoro readout before speaking.
func startCommandTTS(text string, m *Model) tea.Cmd {
	m.killTTS()
	m.ttsGen++
	gen := m.ttsGen
	m.ttsExIdx = -1
	setTTSMicMute(true)
	clog.Debugf("tts: command gen=%d text=%q", gen, text)
	return startTTSSay(text, m.ttsCommandRate, m.c2cfg.TTSCommandVoice, gen, m)
}

// startTTSSay uses macOS say(1).
// A per-call timeout is estimated from text length to guard against say(1) hangs.
func startTTSSay(text string, rate int, voice string, gen int, m *Model) tea.Cmd {
	if rate <= 0 {
		rate = 200
	}
	args := []string{"-r", fmt.Sprintf("%d", rate)}
	if voice != "" {
		args = append(args, "-v", voice)
	}
	// Estimate timeout: expected speech time + 3s grace for say(1) audio drain, min 8s.
	// Multiply before dividing to avoid integer truncation (60/200 = 0).
	words := len([]rune(text))/5 + 1
	secs := words*60/rate + 3
	if secs < 8 {
		secs = 8
	}
	clog.Debugf("tts: say gen=%d rate=%d words~%d timeout=%ds", gen, rate, words, secs)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs)*time.Second)
	cmd := exec.CommandContext(ctx, "say", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = strings.NewReader(text)
	m.ttsCmd = cmd
	t0 := time.Now()
	return func() tea.Msg {
		defer cancel()
		err := cmd.Run()
		elapsed := time.Since(t0).Round(time.Millisecond)
		if err != nil {
			clog.Errorf("tts: say gen=%d done err=%v elapsed=%s", gen, err, elapsed)
		} else {
			clog.Debugf("tts: say gen=%d done elapsed=%s", gen, elapsed)
		}
		return ttsDoneMsg{err: err, gen: gen}
	}
}

// startTTSOsascript uses osascript to invoke macOS speech synthesis.
// Unlike say(1), osascript exits cleanly when speech finishes, so no timeout kills occur.
// Rollback: in startReadoutTTS, replace startTTSKokoroStream with startTTSOsascript.
func startTTSOsascript(text string, rate int, voice string, gen int, m *Model) tea.Cmd {
	if rate <= 0 {
		rate = 200
	}
	// Escape double-quotes for AppleScript string literals (double them).
	escaped := strings.ReplaceAll(text, `"`, `""`)
	var script string
	if voice != "" {
		script = fmt.Sprintf(`say "%s" using "%s" speaking rate %d`, escaped, voice, rate)
	} else {
		script = fmt.Sprintf(`say "%s" speaking rate %d`, escaped, rate)
	}
	// Generous safety timeout only for genuine hangs; osascript should exit cleanly.
	words := len([]rune(text))/5 + 1
	secs := words*60/rate + 10
	if secs < 15 {
		secs = 15
	}
	clog.Debugf("tts: osascript gen=%d rate=%d words~%d timeout=%ds", gen, rate, words, secs)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs)*time.Second)
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	m.ttsCmd = cmd
	t0 := time.Now()
	return func() tea.Msg {
		defer cancel()
		err := cmd.Run()
		elapsed := time.Since(t0).Round(time.Millisecond)
		if err != nil {
			clog.Errorf("tts: osascript gen=%d done err=%v elapsed=%s", gen, err, elapsed)
		} else {
			clog.Debugf("tts: osascript gen=%d done elapsed=%s", gen, elapsed)
		}
		return ttsDoneMsg{err: err, gen: gen}
	}
}

// startTTSKokoro uses the sherpa-onnx Kokoro engine (batch mode, not streaming).
// Rollback: in startReadoutTTS, replace startTTSKokoroStream with startTTSKokoro.
func startTTSKokoro(text string, gen int, m *Model) tea.Cmd {
	// Lazy-initialise the engine on first use.
	if m.ttsEngine == nil {
		eng, err := speech.NewTTSEngine(speech.TTSEngineConfig{
			Model:   m.c2cfg.TTSModel,
			Voices:  m.c2cfg.TTSVoices,
			Tokens:  m.c2cfg.TTSTokens,
			DataDir: m.c2cfg.TTSDataDir,
			Lexicon: m.c2cfg.TTSLexicon,
			Lang:    m.c2cfg.TTSReadoutVoice,
		})
		if err != nil {
			return func() tea.Msg { return ttsDoneMsg{err: err, gen: gen} }
		}
		m.ttsEngine = eng
	}
	stopCh := make(chan struct{})
	m.ttsKokoroStop = stopCh

	eng := m.ttsEngine
	speakerID := m.c2cfg.TTSReadoutSpeakerID
	speed := float32(m.ttsReadoutRate) / 200.0
	if speed <= 0 {
		speed = 1.0
	}

	return func() tea.Msg {
		samples, err := eng.Generate(text, speakerID, speed)
		if err != nil {
			return ttsDoneMsg{err: err, gen: gen}
		}
		err = audio.Play(samples, eng.SampleRate, stopCh)
		return ttsDoneMsg{err: err, gen: gen}
	}
}

// startTTSKokoroStream runs Kokoro inference and plays via PortAudio.
// Rollback: in startReadoutTTS, replace startTTSKokoroStream with startTTSKokoro (batch) or startTTSSay.
func startTTSKokoroStream(text string, gen int, m *Model) tea.Cmd {
	if m.ttsEngine == nil {
		eng, err := speech.NewTTSEngine(speech.TTSEngineConfig{
			Model:   m.c2cfg.TTSModel,
			Voices:  m.c2cfg.TTSVoices,
			Tokens:  m.c2cfg.TTSTokens,
			DataDir: m.c2cfg.TTSDataDir,
			Lexicon: m.c2cfg.TTSLexicon,
			Lang:    m.c2cfg.TTSReadoutVoice,
		})
		if err != nil {
			return func() tea.Msg { return ttsDoneMsg{err: err, gen: gen} }
		}
		m.ttsEngine = eng
	}
	stopCh := make(chan struct{})
	m.ttsKokoroStop = stopCh
	eng := m.ttsEngine
	speakerID := m.c2cfg.TTSReadoutSpeakerID
	speed := float32(m.ttsReadoutRate) / 200.0
	if speed <= 0 {
		speed = 1.0
	}
	clog.Debugf("tts: kokoro-stream gen=%d speed=%.2f", gen, speed)
	t0 := time.Now()
	return func() tea.Msg {
		err := eng.GenerateStreaming(text, speakerID, speed, stopCh)
		elapsed := time.Since(t0).Round(time.Millisecond)
		if err != nil {
			clog.Errorf("tts: kokoro-stream gen=%d done err=%v elapsed=%s", gen, err, elapsed)
		} else {
			clog.Debugf("tts: kokoro-stream gen=%d done elapsed=%s", gen, elapsed)
		}
		return ttsDoneMsg{err: err, gen: gen}
	}
}

// setVoiceState updates the FSM state in the model and notifies the pipeline.
func (m *Model) setVoiceState(s VoiceState) {
	clog.Debugf("fsm: %s → %s", m.voiceState, s)
	m.voiceState = s
	if m.stateChangeCh != nil {
		// Non-blocking send: pipeline drains this before the next audio chunk.
		select {
		case m.stateChangeCh <- s:
		default:
			// Overwrite: drain old value then send new.
			select {
			case <-m.stateChangeCh:
			default:
			}
			m.stateChangeCh <- s
		}
	}
}

// awakeTimeout is how long the FSM waits in AWAKE state before returning to the previous state.
const awakeTimeout = 20 * time.Second

// handleKWSEvent is the main FSM dispatch for keyword events.
func (m *Model) handleKWSEvent(keyword string) []tea.Cmd {
	// Filter by the active keyword set for this state.
	if !m.voiceState.activeKeywords()[keyword] {
		clog.Debugf("fsm: keyword %q ignored in state %s", keyword, m.voiceState)
		return nil
	}
	clog.Debugf("fsm: keyword=%q state=%s", keyword, m.voiceState)

	var cmds []tea.Cmd

	cancelTimer := func(t **time.Timer) {
		if *t != nil {
			(*t).Stop()
			*t = nil
		}
	}
	startAwakeTimer := func() {
		cancelTimer(&m.awakeTimer)
		clog.Debugf("fsm: awake timer set 5s (kws path)")
		m.awakeTimer = time.AfterFunc(5*time.Second, func() {
			if programSend != nil {
				programSend(voiceAwakeTimeoutMsg{})
			}
		})
	}
	switch m.voiceState {

	case VoiceIdle:
		if keyword == "computer" {
			m.suspendTTS()
			m.voiceReturnState = VoiceIdle
			m.voiceFailCount = 0
			m.setVoiceState(VoiceAwake)
			startAwakeTimer()
			go playBeep(beepWakeAck)
		}

	case VoiceAwake:
		if keyword == "computer" {
			// Already awake — ignore duplicate wake word.
			clog.Debugf("fsm: computer ignored (already AWAKE)")
		} else {
			cancelTimer(&m.awakeTimer)
			cmds = append(cmds, m.executeAwakeCommand(keyword)...)
		}

	case VoiceDictating:
		switch keyword {
		case "computer":
			// Interrupt → AWAKE; preserve input and dictation intent.
			m.pendingVoiceSubmit = false
			m.voiceReturnState = VoiceDictating
			m.voiceFailCount = 0
			m.setVoiceState(VoiceAwake)
			cancelTimer(&m.awakeTimer)
			clog.Debugf("fsm: awake timer set %s (interrupt from DICTATING)", awakeTimeout)
			m.awakeTimer = time.AfterFunc(awakeTimeout, func() {
				if programSend != nil {
					programSend(voiceAwakeTimeoutMsg{})
				}
			})
			go playBeep(beepWakeAck)
		}

	case VoiceExecuting:
		if keyword == "computer" {
			cancelTimer(&m.executingTimer)
			m.suspendTTS()
			m.voiceReturnState = VoiceExecuting
			m.voiceFailCount = 0
			m.setVoiceState(VoiceAwake)
			clog.Debugf("fsm: awake timer set %s (interrupt from EXECUTING)", awakeTimeout)
			m.awakeTimer = time.AfterFunc(awakeTimeout, func() {
				if programSend != nil {
					programSend(voiceAwakeTimeoutMsg{})
				}
			})
			go playBeep(beepWakeAck)
		}

	case VoiceConversing:
		switch keyword {
		case "computer":
			m.suspendTTS()
			m.voiceReturnState = VoiceConversing
			m.voiceFailCount = 0
			m.setVoiceState(VoiceAwake)
			clog.Debugf("fsm: awake timer set %s (interrupt from CONVERSING)", awakeTimeout)
			m.awakeTimer = time.AfterFunc(awakeTimeout, func() {
				if programSend != nil {
					programSend(voiceAwakeTimeoutMsg{})
				}
			})
			go playBeep(beepWakeAck)
		}
	}

	m.rebuildConvContent()
	return cmds
}

// handleResourceKey handles all keyboard input when the resource overlay is active.
func (m *Model) handleResourceKey(msg tea.KeyMsg) []tea.Cmd {
	var cmds []tea.Cmd

	switch {
	case key.Matches(msg, keys.Cancel): // Ctrl+C
		if m.isTTSPlaying() {
			m.killTTS()
			m.rebuildResourceContent()
		} else {
			m.closeResourceOverlay()
		}

	case msg.Type == tea.KeyCtrlX:
		m.killTTS()
		m.closeResourceOverlay()

	case msg.Type == tea.KeyRunes && string(msg.Runes) == "s":
		if m.isTTSPlaying() {
			m.killTTS()
			m.resourceTTSText = ""
			m.rebuildResourceContent()
		} else {
			// Build queue of non-empty line indices from cursor onward.
			var queue []int
			for i := m.resourceCursor; i < len(m.resourceLines); i++ {
				if ttsStrip(m.resourceLines[i]) != "" {
					queue = append(queue, i)
				}
			}
			if len(queue) > 0 {
				m.resourceTTSQueue = queue[1:]
				m.resourceCursor = queue[0]
				m.resourceTTSText = m.resourceLines[queue[0]]
				m.rebuildResourceContent()
				m.scrollResourceToCursor()
				cmds = append(cmds, startReadoutTTS(ttsStrip(m.resourceTTSText), -1, m))
			}
		}

	case msg.Type == tea.KeyRunes && string(msg.Runes) == "[":
		if m.isTTSPlaying() && m.resourceTTSText != "" {
			if m.ttsReadoutRate > 80 {
				m.ttsReadoutRate -= 20
			}
			text := m.resourceTTSText
			m.resourceTTSQueue = m.resourceTTSQueue // preserve queue
			killTTSAudio(m)
			cmds = append(cmds, startReadoutTTS(ttsStrip(text), -1, m))
		}

	case msg.Type == tea.KeyRunes && string(msg.Runes) == "]":
		if m.isTTSPlaying() && m.resourceTTSText != "" {
			if m.ttsReadoutRate < 500 {
				m.ttsReadoutRate += 20
			}
			text := m.resourceTTSText
			m.resourceTTSQueue = m.resourceTTSQueue // preserve queue
			killTTSAudio(m)
			cmds = append(cmds, startReadoutTTS(ttsStrip(text), -1, m))
		}

	case msg.Type == tea.KeyRunes && string(msg.Runes) == "e":
		editor := os.Getenv("EDITOR")
		if editor == "" {
			m.lastCmd = &cmdResult{
				input:   "resource-edit",
				output:  []string{"$EDITOR is not set or not exported — add 'export EDITOR=<path>' to your shell config"},
				isError: true,
			}
			m.cmdPaneOpen = true
			m.cmdScroll.SetContent(renderCmdOutput(m))
			m.cmdScroll.GotoTop()
			m.syncLayout()
			break
		}
		m.killTTS()
		filePath := filepath.Join(m.eng.ResourceDir(), m.resourceName)
		name := m.resourceName
		editorCmd := exec.Command(editor, filePath)
		cmds = append(cmds, tea.ExecProcess(editorCmd, func(err error) tea.Msg {
			return resourceReloadMsg{name: name}
		}))

	case msg.Type == tea.KeyRunes && string(msg.Runes) == "g":
		m.resourceCursor = 0
		m.rebuildResourceContent()
		m.resourceScroll.GotoTop()

	case msg.Type == tea.KeyRunes && string(msg.Runes) == "G":
		if len(m.resourceLines) > 0 {
			m.resourceCursor = len(m.resourceLines) - 1
		}
		m.rebuildResourceContent()
		m.resourceScroll.GotoBottom()

	case key.Matches(msg, keys.NavUp):
		if m.resourceCursor > 0 {
			m.resourceCursor--
			m.rebuildResourceContent()
			m.scrollResourceToCursor()
		}

	case key.Matches(msg, keys.NavDown):
		if m.resourceCursor < len(m.resourceLines)-1 {
			m.resourceCursor++
			m.rebuildResourceContent()
			m.scrollResourceToCursor()
		}

	case key.Matches(msg, keys.ScrollUp): // PgUp
		step := m.resourceScroll.Height / 2
		if step < 1 {
			step = 1
		}
		m.resourceCursor -= step
		if m.resourceCursor < 0 {
			m.resourceCursor = 0
		}
		m.rebuildResourceContent()
		m.resourceScroll.HalfPageUp()
		m.scrollResourceToCursor()

	case key.Matches(msg, keys.ScrollDown): // PgDn
		step := m.resourceScroll.Height / 2
		if step < 1 {
			step = 1
		}
		m.resourceCursor += step
		if m.resourceCursor >= len(m.resourceLines) {
			m.resourceCursor = len(m.resourceLines) - 1
		}
		m.rebuildResourceContent()
		m.resourceScroll.HalfPageDown()
		m.scrollResourceToCursor()
	}

	return cmds
}

// executeAwakeCommand dispatches a command label from the AWAKE state.
// Called by both handleKWSEvent (KWS path) and voiceTranscriptMsg (STT path)
// so the activeKeywords filter is not applied here.
func (m *Model) executeAwakeCommand(label string) []tea.Cmd {
	var cmds []tea.Cmd

	startExecutingTimer := func() {
		if m.executingTimer != nil {
			m.executingTimer.Stop()
		}
		clog.Debugf("fsm: executing timer set 5s")
		m.executingTimer = time.AfterFunc(5*time.Second, func() {
			clog.Debugf("fsm: executing timer expired after 5s")
			if programSend != nil {
				programSend(voiceExecutingTimeoutMsg{})
			}
		})
	}

	switch label {
	case "clear":
		m.killTTS()
		m.voiceSession = false
		m.pendingAction = nil
		m.pendingPost = nil
		m.confirmBuf = ""
		m.pendingDictCmd = ""
		m.pendingVoiceSubmit = false
		m.transcribing = false
		m.input.Reset()
		m.input.SetHeight(1)
		m.pastedBlob = ""
		m.completionItems = nil
		m.completionIdx = -1
		m.paramItems = nil
		m.paramIdx = -1
		m.cmdPaneOpen = false
		m.lastCmd = nil
		m.focusedExIdx = -1
		m.deletingExIdx = -1
		m.setFocus(paneInput)
		m.input.Focus()
		m.setVoiceState(VoiceIdle)
		m.syncLayout()
		m.rebuildConvContent()
		go playBeep(beepCmdAck)

	case "stop", "stop_playback":
		m.voiceSession = false
		m.killTTS()
		if m.pendingAction != nil {
			m.pendingAction = nil
			m.pendingPost = nil
			m.confirmBuf = ""
			m.focusedExIdx = -1
			m.deletingExIdx = -1
			canceled := cmdResult{input: m.lastCmd.input, output: []string{"operation canceled"}}
			m.lastCmd = &canceled
			m.cmdScroll.SetContent(renderCmdOutput(m))
			m.cmdScroll.GotoTop()
			m.setFocus(paneInput)
			m.input.Focus()
			m.syncLayout()
		}
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "resume", "resume_playback":
		cmds = append(cmds, m.resumeTTS())
		m.setVoiceState(VoiceExecuting)
		startExecutingTimer()
		go playAck()

	case "session_start", "talk_start":
		m.voiceSession = true
		m.setVoiceState(VoiceConversing)
		// go playBeep(beepWakeAck) — replaced by spoken "Conversing"
		cmds = append(cmds, startCommandTTS("Conversing", m))

	case "session_resume":
		if cmd := m.resumeTTS(); cmd != nil {
			// Suspended TTS exists — restart it and stay in EXECUTING.
			cmds = append(cmds, cmd)
			m.setVoiceState(VoiceExecuting)
			startExecutingTimer()
		} else if m.voiceSession {
			m.setVoiceState(VoiceConversing)
		} else {
			m.setVoiceState(m.voiceReturnState)
		}
		go playAck()

	case "clear_input":
		if m.voiceReturnState == VoiceDictating && m.pendingDictCmd == "note" {
			m.input.SetValue("// ")
		} else {
			m.input.SetValue("")
		}
		m.input.CursorEnd()
		m.syncLayout()
		m.setVoiceState(m.voiceReturnState)
		go playBeep(beepDictStart)

	case "session_end", "talk_end":
		m.voiceSession = false
		m.killTTS()
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "ask_llm":
		m.pendingDictCmd = "llm"
		m.input.SetValue("")
		m.setVoiceState(VoiceDictating)
		go playBeep(beepDictStart)

	case "chat_note":
		m.pendingDictCmd = "note"
		m.input.SetValue("// ")
		m.setVoiceState(VoiceDictating)
		// go playBeep(beepDictStart) — replaced by spoken "Dictating"
		cmds = append(cmds, startCommandTTS("Dictating", m))

	case "chat_replay", "chat_play_last":
		if len(m.exchanges) > 0 {
			last := len(m.exchanges) - 1
			enqueueTTSText(ttsText(&m.exchanges[last]), last, m, &cmds)
		}
		m.setVoiceState(VoiceExecuting)
		startExecutingTimer()
		go playAck()

	case "chat_play_all":
		for i := range m.exchanges {
			m.ttsQueue = append(m.ttsQueue, i)
		}
		if len(m.ttsQueue) > 0 {
			next := m.ttsQueue[0]
			m.ttsQueue = m.ttsQueue[1:]
			enqueueTTSText(ttsText(&m.exchanges[next]), next, m, &cmds)
		}
		m.setVoiceState(VoiceExecuting)
		startExecutingTimer()
		go playAck()

	case "config":
		m.submitVoiceSlashCmd("/config")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "voice_status":
		returnState := m.voiceReturnState
		topic := m.eng.TopicName()
		model := m.eng.Profile().Model
		m.setVoiceState(m.voiceReturnState)
		go sayVoiceStatus(returnState, topic, model)

	case "status":
		m.submitVoiceSlashCmd("/status")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "stats":
		m.submitVoiceSlashCmd("/stats")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "voice_commands":
		m.submitVoiceSlashCmd("/voice-commands")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "fold":
		m.submitVoiceSlashCmd("/fold")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "unfold":
		m.submitVoiceSlashCmd("/unfold")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "play_all":
		m.submitVoiceSlashCmd("/play-all")
		// submitVoiceSlashCmd does not have the TTS kickoff the typed-Enter path
		// has, so we must start playback explicitly here.
		if !m.isTTSPlaying() && len(m.ttsQueue) > 0 {
			next := m.ttsQueue[0]
			m.ttsQueue = m.ttsQueue[1:]
			if next < len(m.exchanges) {
				enqueueTTSText(ttsText(&m.exchanges[next]), next, m, &cmds)
			}
		}
		m.setVoiceState(VoiceExecuting)
		startExecutingTimer()

	case "tts_toggle":
		m.submitVoiceSlashCmd("/tts")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "topic_info":
		m.submitVoiceSlashCmd("/topic")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "topic_default":
		m.submitVoiceSlashCmd("/topic-default")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "profile_info":
		m.submitVoiceSlashCmd("/profile")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "profile_default":
		m.submitVoiceSlashCmd("/profile-default")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "system_show":
		m.submitVoiceSlashCmd("/system")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "system_clear":
		m.submitVoiceSlashCmd("/system-clear")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "help":
		m.submitVoiceSlashCmd("/help")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "delete_last":
		if len(m.exchanges) == 0 {
			m.setVoiceState(VoiceIdle)
			cmds = append(cmds, startCommandTTS("Nothing to delete", m))
		} else {
			_, err := m.eng.DeleteLast(1)
			if err == nil {
				m.exchanges = nil
				m.loadHistory()
				m.deletingExIdx = -1
				m.rebuildConvContent()
				m.syncLayout()
			}
			m.setVoiceState(VoiceIdle)
			go playAck()
			if err == nil {
				cmds = append(cmds, startCommandTTS("Deleted", m))
			}
		}

	case "topic_summary":
		m.submitVoiceSlashCmd("/topic-summary")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "topic_history":
		m.submitVoiceSlashCmd("/topic-history")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "topic_list":
		m.submitVoiceSlashCmd("/topic-list")
		m.setVoiceState(VoiceIdle)
		if m.lastCmd != nil && m.lastCmd.spokenText != "" {
			cmds = append(cmds, startCommandTTS(m.lastCmd.spokenText, m))
		} else {
			go playAck()
		}

	case "topic_clear":
		m.submitVoiceSlashCmd("/topic-clear")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "topic_delete":
		m.prefillVoiceCmd("/topic-delete")

	case "topic_switch":
		m.prefillVoiceCmd("/topic-switch")

	case "topic_default_set":
		m.prefillVoiceCmd("/topic-default-set")

	case "topic_new":
		m.pendingDictCmd = label
		m.input.SetValue("")
		m.setVoiceState(VoiceDictating)
		go playBeep(beepDictStart)

	case "profile_list":
		m.submitVoiceSlashCmd("/profile-list")
		m.setVoiceState(VoiceIdle)
		if m.lastCmd != nil && m.lastCmd.spokenText != "" {
			cmds = append(cmds, startCommandTTS(m.lastCmd.spokenText, m))
		} else {
			go playAck()
		}

	case "profile_switch":
		m.prefillVoiceCmd("/profile-switch")

	case "profile_default_set":
		m.prefillVoiceCmd("/profile-default-set")

	case "resource_list":
		m.submitVoiceSlashCmd("/resource-list")
		m.setVoiceState(VoiceIdle)
		if m.lastCmd != nil && m.lastCmd.spokenText != "" {
			cmds = append(cmds, startCommandTTS(m.lastCmd.spokenText, m))
		} else {
			go playAck()
		}

	case "resource_remove":
		m.prefillVoiceCmd("/resource-remove")

	case "resource_view":
		m.prefillVoiceCmd("/resource-view")

	case "resource_edit":
		m.prefillVoiceCmd("/resource-edit")

	case "export":
		m.prefillVoiceCmd("/export")

	case "open_log":
		m.submitVoiceSlashCmd("/log")
		m.setVoiceState(VoiceIdle)
		go playAck()

	case "copy_input":
		text := m.input.Value()
		if m.pendingDictCmd == "note" {
			text = strings.TrimSpace(strings.TrimPrefix(text, "//"))
		}
		if text != "" {
			cmd := exec.Command("pbcopy")
			cmd.Stdin = strings.NewReader(text)
			_ = cmd.Run()
			cmds = append(cmds, startCommandTTS("Copied", m))
		} else {
			go playBeep(beepUnrecognized)
		}
		m.setVoiceState(m.voiceReturnState)

	case "correct_input":
		text := strings.TrimSpace(m.input.Value())
		if m.correcting || text == "" {
			m.setVoiceState(m.voiceReturnState)
			go playBeep(beepUnrecognized)
			break
		}
		// Strip "//" note prefix before correcting.
		if strings.HasPrefix(text, "//") {
			text = strings.TrimSpace(text[2:])
			m.input.SetValue("// " + text)
		}
		m.correctionVoiceState = m.voiceReturnState
		m.setVoiceState(VoiceIdle)
		m.correcting = true
		go playAck()
		cmds = append(cmds, doCorrection(text, m.cfg, m.c2cfg))

	default:
		// Unrecognised label — return to previous state.
		clog.Debugf("fsm: unrecognised command %q, returning to %s", label, m.voiceReturnState)
		m.setVoiceState(m.voiceReturnState)
		go playBeep(beepUnrecognized)
	}

	m.rebuildConvContent()
	return cmds
}

// saveVoiceNote saves the current input as a note entry (same as // prefix).
func (m *Model) saveVoiceNote() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	m.input.SetValue("")
	m.pendingDictCmd = ""
	if text == "" {
		m.setVoiceState(VoiceIdle)
		go playBeep(beepUnrecognized)
		return nil
	}
	if err := m.eng.AddNote(text); err == nil {
		m.exchanges = append(m.exchanges, exchange{
			userMsg:  core.Message{Role: core.RoleNote, Content: text, Time: time.Now()},
			isNote:   true,
			complete: true,
			expanded: true,
		})
	}
	m.setVoiceState(VoiceIdle)
	m.rebuildConvContent()
	go playAck()
	return nil
}

// runSlashCmd executes a slash command string and returns the result.
// Caller must apply the result to the model via applySlashResult.
func (m *Model) runSlashCmd(cmd string) cmdResult {
	return handleCommand(m, cmd)
}

// applySlashResult updates the model with slash command output.
func (m *Model) applySlashResult(res cmdResult) {
	m.lastCmd = &res
	m.cmdPaneOpen = true
	m.setFocus(paneCmd)
	m.input.Blur()
	m.syncLayout()
	m.cmdScroll.SetContent(renderCmdOutput(m))
	m.cmdScroll.GotoTop()
}

// submitVoiceSlashCmd places cmd visibly in the input box, pushes it to
// history, then executes it — giving the same visual trace as typing + Enter.
func (m *Model) submitVoiceSlashCmd(cmd string) {
	m.input.SetValue(cmd)
	m.input.CursorEnd()
	m.pushHistory(cmd)
	res := handleCommand(m, cmd)
	m.applySlashResult(res)
}

// prefillVoiceCmd pre-fills the input with "/cmd " and opens the param picker,
// returning focus to the input pane — voice as a keyboard accelerator.
func (m *Model) prefillVoiceCmd(cmd string) {
	m.input.SetValue(cmd + " ")
	m.input.CursorEnd()
	params := contextualParams(m, cmd)
	m.paramItems = params
	if len(params) > 0 {
		m.paramIdx = 0
	} else {
		m.paramIdx = -1
	}
	m.setFocus(paneInput)
	m.input.Focus()
	m.setVoiceState(m.voiceReturnState)
	go playBeep(beepDictStart)
}

// Beep tone identifiers.
const (
	beepWakeAck      = "wake"
	beepCmdAck       = "cmd"
	beepDictStart    = "dict_start"
	beepDictEnd      = "dict_end"
	beepUnrecognized = "unrecognized"
)

// sayAck speaks "OK" via macOS say(1) as a command acknowledgement.
func sayAck() {
	t0 := time.Now()
	clog.Debugf("beep: say OK start")
	args := []string{"OK"}
	if activeC2Cfg.TTSCommandVoice != "" {
		args = append([]string{"-v", activeC2Cfg.TTSCommandVoice}, args...)
	}
	cmd := exec.Command("say", args...)
	err := cmd.Run()
	if err != nil {
		clog.Errorf("beep: say OK error: %v (elapsed %s)", err, time.Since(t0).Round(time.Millisecond))
	} else {
		clog.Debugf("beep: say OK done (%s)", time.Since(t0).Round(time.Millisecond))
	}
}

// sayPrompt speaks an encouragement phrase after a failed command recognition.
// n is the 1-based miss count; alternates between two phrases.
func sayPrompt(n int) {
	var phrase string
	if n%2 == 1 {
		phrase = "Try again?"
	} else {
		phrase = "Say again?"
	}
	t0 := time.Now()
	clog.Debugf("beep: say prompt n=%d %q start", n, phrase)
	args := []string{phrase}
	if activeC2Cfg.TTSCommandVoice != "" {
		args = append([]string{"-v", activeC2Cfg.TTSCommandVoice}, args...)
	}
	cmd := exec.Command("say", args...)
	err := cmd.Run()
	if err != nil {
		clog.Errorf("beep: say prompt error: %v (elapsed %s)", err, time.Since(t0).Round(time.Millisecond))
	} else {
		clog.Debugf("beep: say prompt done (%s)", time.Since(t0).Round(time.Millisecond))
	}
}

// sayVoiceStatus speaks the current FSM context via macOS say(1).
func sayVoiceStatus(returnState VoiceState, topic, model string) {
	var state string
	switch returnState {
	case VoiceIdle:
		state = "Idle"
	case VoiceDictating:
		state = "Dictating"
	case VoiceConversing:
		state = "Conversing"
	case VoiceExecuting:
		state = "Executing"
	default:
		state = "Ready"
	}
	phrase := fmt.Sprintf("%s. Topic: %s. Model: %s.", state, topic, model)
	t0 := time.Now()
	clog.Debugf("beep: say voice status %q start", phrase)
	args := []string{phrase}
	if activeC2Cfg.TTSCommandVoice != "" {
		args = append([]string{"-v", activeC2Cfg.TTSCommandVoice}, args...)
	}
	cmd := exec.Command("say", args...)
	err := cmd.Run()
	if err != nil {
		clog.Errorf("beep: say voice status error: %v (elapsed %s)", err, time.Since(t0).Round(time.Millisecond))
	} else {
		clog.Debugf("beep: say voice status done (%s)", time.Since(t0).Round(time.Millisecond))
	}
}

// beepSoftChime plays a soft 880Hz tone with a linear fade-out envelope.
func beepSoftChime() {
	t0 := time.Now()
	clog.Debugf("beep: soft chime start")
	const sampleRate = 16000
	const freq = 880.0
	const durationMs = 200
	n := sampleRate * durationMs / 1000
	samples := make([]float32, n)
	const amplitude = 0.35
	const pi2 = 2 * 3.14159265358979
	for i := range samples {
		fade := 1.0 - float64(i)/float64(n) // linear fade out
		samples[i] = float32(amplitude * fade * sin(pi2*freq*float64(i)/float64(sampleRate)))
	}
	stopCh := make(chan struct{})
	err := audio.Play(samples, sampleRate, stopCh)
	if err != nil {
		clog.Errorf("beep: soft chime audio.Play error: %v (elapsed %s)", err, time.Since(t0).Round(time.Millisecond))
	} else {
		clog.Debugf("beep: soft chime done (%s)", time.Since(t0).Round(time.Millisecond))
	}
}

// beepEarcon plays a two-note C5→E5 motif (~60ms each).
func beepEarcon() {
	t0 := time.Now()
	clog.Debugf("beep: earcon start")
	const sampleRate = 16000
	const durationMs = 70
	const amplitude = 0.30
	const pi2 = 2 * 3.14159265358979
	freqs := []float64{523.25, 659.25} // C5, E5
	n := sampleRate * durationMs / 1000
	samples := make([]float32, n*len(freqs))
	for ni, freq := range freqs {
		offset := ni * n
		for i := 0; i < n; i++ {
			// Short fade in (10%) and fade out (20%) to avoid clicks.
			var env float64
			switch {
			case i < n/10:
				env = float64(i) / float64(n/10)
			case i > n*4/5:
				env = float64(n-i) / float64(n/5)
			default:
				env = 1.0
			}
			samples[offset+i] = float32(amplitude * env * sin(pi2*freq*float64(i)/float64(sampleRate)))
		}
	}
	stopCh := make(chan struct{})
	err := audio.Play(samples, sampleRate, stopCh)
	if err != nil {
		clog.Errorf("beep: earcon audio.Play error: %v (elapsed %s)", err, time.Since(t0).Round(time.Millisecond))
	} else {
		clog.Debugf("beep: earcon done (%s)", time.Since(t0).Round(time.Millisecond))
	}
}

// playAck is the command-acknowledgement sound. Switch the call to compare options.
func playAck() {
	clog.Debugf("beep: ack (earcon)")
	beepEarcon() // options: sayAck(), beepSoftChime(), beepEarcon()
}

// playBeep plays a short audio tone for the given event. Runs in a goroutine.
// First pass: simple sine-wave tones generated inline via the audio package.
func playBeep(kind string) {
	t0 := time.Now()
	clog.Debugf("beep: %s start", kind)
	const sampleRate = 16000
	type tone struct{ freq float64; durationMs int }
	var t tone
	switch kind {
	case beepWakeAck:
		t = tone{880, 120}
	case beepCmdAck:
		t = tone{660, 80}
	case beepDictStart:
		t = tone{523, 150} // rising: C5
	case beepDictEnd:
		t = tone{392, 150} // falling: G4
	default: // beepUnrecognized
		t = tone{300, 180}
	}
	samples := makeSineTone(t.freq, t.durationMs, sampleRate)
	stopCh := make(chan struct{})
	err := audio.Play(samples, sampleRate, stopCh)
	if err != nil {
		clog.Errorf("beep: %s audio.Play error: %v (elapsed %s)", kind, err, time.Since(t0).Round(time.Millisecond))
	} else {
		clog.Debugf("beep: %s done (%s)", kind, time.Since(t0).Round(time.Millisecond))
	}
}

// makeSineTone generates a sine-wave PCM buffer at the given frequency.
func makeSineTone(freqHz float64, durationMs int, sampleRate int) []float32 {
	n := sampleRate * durationMs / 1000
	samples := make([]float32, n)
	const amplitude = 0.3
	const pi2 = 2 * 3.14159265358979
	for i := range samples {
		samples[i] = float32(amplitude * sin(pi2*freqHz*float64(i)/float64(sampleRate)))
	}
	return samples
}

// sin is a simple Taylor-series sine approximation to avoid importing math.
// For short tones the accuracy is sufficient.
func sin(x float64) float64 {
	// Reduce to [-π, π]
	const pi = 3.14159265358979
	for x > pi {
		x -= 2 * pi
	}
	for x < -pi {
		x += 2 * pi
	}
	// 5-term Taylor series
	x2 := x * x
	return x * (1 - x2/6*(1-x2/20*(1-x2/42)))
}

func calcExchangeCost(r engine.ChatResult, eng *engine.Engine) float64 {
	inPer1M, outPer1M, ok := config.ExtractPricing(eng.Profile().Info)
	if !ok {
		return 0
	}
	return config.CalcCost(r.Usage.InputTokens, r.Usage.OutputTokens, inPer1M, outPer1M)
}

const topicPickerMaxVisible = 8

// handleTopicPickerKey processes keyboard input when the topic picker overlay is active.
func (m *Model) handleTopicPickerKey(msg tea.KeyMsg) []tea.Cmd {
	switch {
	case key.Matches(msg, keys.Cancel), key.Matches(msg, keys.Dismiss), key.Matches(msg, keys.CloseOverlay):
		m.closeTopicPicker()

	case key.Matches(msg, keys.SwitchTopic):
		m.closeTopicPicker()

	case key.Matches(msg, keys.Send):
		if len(m.topicPickerItems) > 0 {
			name := m.topicPickerItems[m.topicPickerIdx]
			m.closeTopicPicker()
			res := cmdTopicSwitch(m, []string{name})
			if !res.isError {
				m.lastCmd = &res
				m.cmdPaneOpen = false
			}
		}

	case key.Matches(msg, keys.NavUp):
		if m.topicPickerIdx > 0 {
			m.topicPickerIdx--
			if m.topicPickerIdx < m.topicPickerScroll {
				m.topicPickerScroll = m.topicPickerIdx
			}
		}

	case key.Matches(msg, keys.NavDown):
		if m.topicPickerIdx < len(m.topicPickerItems)-1 {
			m.topicPickerIdx++
			if m.topicPickerIdx >= m.topicPickerScroll+topicPickerMaxVisible {
				m.topicPickerScroll = m.topicPickerIdx - topicPickerMaxVisible + 1
			}
		}

	case msg.Type == tea.KeyBackspace:
		if len(m.topicPickerFilter) > 0 {
			runes := []rune(m.topicPickerFilter)
			m.topicPickerFilter = string(runes[:len(runes)-1])
			m.filterTopicPicker()
		}

	case msg.Type == tea.KeyRunes:
		m.topicPickerFilter += string(msg.Runes)
		m.filterTopicPicker()
	}
	return nil
}

// handleProfilePickerKey processes keyboard input when the profile picker overlay is active.
func (m *Model) handleProfilePickerKey(msg tea.KeyMsg) []tea.Cmd {
	switch {
	case key.Matches(msg, keys.Cancel), key.Matches(msg, keys.Dismiss), key.Matches(msg, keys.CloseOverlay):
		m.closeProfilePicker()

	case key.Matches(msg, keys.SwitchProfile):
		m.closeProfilePicker()

	case key.Matches(msg, keys.Send):
		if len(m.profilePickerItems) > 0 {
			name := m.profilePickerItems[m.profilePickerIdx]
			m.closeProfilePicker()
			res := cmdProfileSwitch(m, []string{name})
			if !res.isError {
				m.lastCmd = &res
				m.cmdPaneOpen = false
			}
		}

	case key.Matches(msg, keys.NavUp):
		if m.profilePickerIdx > 0 {
			m.profilePickerIdx--
			if m.profilePickerIdx < m.profilePickerScroll {
				m.profilePickerScroll = m.profilePickerIdx
			}
		}

	case key.Matches(msg, keys.NavDown):
		if m.profilePickerIdx < len(m.profilePickerItems)-1 {
			m.profilePickerIdx++
			if m.profilePickerIdx >= m.profilePickerScroll+topicPickerMaxVisible {
				m.profilePickerScroll = m.profilePickerIdx - topicPickerMaxVisible + 1
			}
		}

	case msg.Type == tea.KeyBackspace:
		if len(m.profilePickerFilter) > 0 {
			runes := []rune(m.profilePickerFilter)
			m.profilePickerFilter = string(runes[:len(runes)-1])
			m.filterProfilePicker()
		}

	case msg.Type == tea.KeyRunes:
		m.profilePickerFilter += string(msg.Runes)
		m.filterProfilePicker()
	}
	return nil
}
