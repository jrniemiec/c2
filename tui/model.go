package tui

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jrniemiec/c2/c2config"
	"github.com/jrniemiec/c2/speech"
	"github.com/jrniemiec/c2/config"
	"github.com/jrniemiec/c2/core"
	"github.com/jrniemiec/c2/engine"
	"github.com/jrniemiec/c2/store"
)

// focusPane identifies which pane has keyboard focus.
type focusPane int

const (
	paneInput focusPane = iota
	paneConv
	paneCmd
	paneResource
	paneTopicPicker
	paneProfilePicker
)

// interactionMode is the active input mode.
type interactionMode int

const (
	modeText  interactionMode = iota // keyboard only
	modeVoice                        // microphone → VAD → STT
)

// exchange holds one complete user+assistant turn, or a standalone note.
type exchange struct {
	userMsg  core.Message
	asstMsg  core.Message // empty while streaming or for notes
	model    string       // model that produced the assistant reply
	costUSD  float64
	elapsed  time.Duration
	complete bool // false while assistant reply is still streaming
	isNote   bool // true for standalone note entries
	isPasted bool // true when user message was a clipboard paste
	expanded bool // true when pasted content is shown in full (in-memory only)
}

// Bubbletea message types.
type streamDeltaMsg string
type correctionDoneMsg struct {
	text    string
	origLen int // length of original text, to detect changes
	err     error
}
type correctionFlashMsg struct{}
type streamDoneMsg struct {
	result engine.ChatResult
	err    error
}
type spinnerTickMsg struct{}
type ttsDoneMsg struct {
	err error
	gen int // generation counter; ignored if != model's current ttsGen
}

// Voice pipeline message types.
type voiceSpeechStartedMsg struct{} // VAD detected speech onset
type voiceTranscriptMsg struct {
	text  string // partial or final transcript
	final bool   // true = utterance complete, ready to submit
}
type voicePipelineErrMsg struct{ err error } // pipeline failed to start or crashed
type voiceAutoSubmitMsg struct{}              // fires 500ms after transcript to auto-send
type clipboardFlashMsg struct{}              // fires 1.5s after copy to clear the flash
type resourceReloadMsg struct{ name string } // fires after external editor exits
type systemReloadMsg struct{}               // fires after system prompt editor exits

// Model is the root Bubbletea application model.
type Model struct {
	eng      *engine.Engine
	cfg      config.Config
	c2cfg    c2config.C2Config
	dataDir string

	// theme
	themeMode  string // "auto", "light", "dark"
	chatLabels bool   // prefix turns with [you]: / [profile]:
	foldLines  int    // lines threshold before folding (0 = never)
	foldOnStart bool  // start with long entries folded

	// layout (set by WindowSizeMsg)
	width  int
	height int

	// panes
	conv  viewport.Model
	input textarea.Model
	focus focusPane

	// conversation
	exchanges    []exchange
	focusedExIdx int // index of focused exchange when paneConv is active; -1 = none
	streaming    bool
	streamBuf    string
	cancelStream context.CancelFunc

	// spinner: pulsating snowflake ❄ bold/dim alternating
	spinnerFrame  int
	windowFocused bool // true when the terminal window has OS focus
	// cursor blink: toggled on every spinner tick (400 ms → 800 ms period)
	cursorVisible bool

	// userScrolled is true when the user has manually scrolled up, suppressing
	// the automatic GotoBottom() that normally keeps the latest content visible.
	userScrolled bool

	// status
	lastResult   *engine.ChatResult
	topicStats   store.CallStats
	sessionStats store.CallStats
	// usageByTs maps unix-nanosecond timestamp → usage entry for the active topic.
	// Used to match history exchanges to their exact logged cost/tokens.
	usageByTs map[int64]store.UsageEntry

	// bottom pane: command output
	lastCmd     *cmdResult
	cmdPaneOpen bool
	cmdScroll   viewport.Model

	// ctrl+c double-press
	lastCtrlC time.Time

	// pending confirmation (e.g. topic-delete, topic-clear)
	pendingAction func() cmdResult
	pendingPost   func(*Model) // model mutation to run after pendingAction, on the real model
	confirmBuf    string

	// input history (bash-style, in-memory only)
	inputHistory []string // oldest first, max 128
	historyIdx   int      // -1 = not browsing
	historySaved string   // draft saved before browsing started

	// paste mode (active when clipboard content exceeds threshold)
	pastedBlob string // full text to send; empty = not in paste mode

	// input correction (Ctrl+R)
	correcting           bool       // true while correction LLM call is in flight
	correctionVoiceState VoiceState // voice state to restore after correction
	correctionFlash      string     // non-empty: flash message shown in status bar

	// deletion behaviour
	ackAllDeletions bool // when true, all deletions require confirmation (--ack-all-deletions)

	// log viewer
	logViewerOpen bool // true while the external log tail window is open

	// voice correction
	speakCorrectedNote   bool // when true, speak "After correction: <text>" via TTS after Ctrl+G
	correctionTTSPending bool // true while waiting for correction TTS to finish before restoring voice state

	// c2 interaction mode
	mode         interactionMode
	transcribing bool // true while VAD/STT is processing speech

	// voice pipeline (non-nil when voice mode has been initialised)
	voiceReady          bool         // true if VAD+STT loaded successfully
	voiceErr            string       // non-empty if pipeline failed to init
	stopVoice           chan struct{} // closed to stop the capture goroutine
	pendingVoiceSubmit  bool         // true while 500ms auto-submit timer is running

	// mic level indicator — two peaks with different decay rates
	voicePeakInner float32 // inner positions (1,2): slow decay ×0.90
	voicePeakOuter float32 // outer positions (0,3): fast decay ×0.72

	// voice FSM
	voiceState       VoiceState
	voiceReturnState VoiceState       // state to return to if AWAKE fails (timeout/unrecognized)
	deletingExIdx    int              // index of entry being voice-deleted (-1 = none)
	voiceFailCount   int              // consecutive STT misses in current AWAKE session
	stateChangeCh    chan VoiceState  // TUI → pipeline; buffered 1
	voiceFlushCh     chan struct{}    // TUI → pipeline: flush accumulated audio now ("over")
	awakeTimer      *time.Timer      // 5s timeout in AWAKE state
	executingTimer  *time.Timer      // 5s timeout in EXECUTING state
	suspended       *suspendedTTS    // non-nil when TTS was interrupted by "computer"
	voiceSession    bool             // true while in continuous conversation (VoiceConversing)
	// pendingDictCmd holds the action to take when DICTATING completes.
	// "llm" = submit to LLM, "note" = save as note, "" = none
	pendingDictCmd  string

	// TTS playback
	ttsCmd          *exec.Cmd         // non-nil while say(1) TTS is playing
	ttsKokoroStop   chan struct{}      // closed to interrupt Kokoro playback
	ttsEngine       *speech.TTSEngine // lazy-initialised Kokoro engine (nil until first use)
	ttsExIdx        int               // exchange being spoken (-1 = none)
	ttsQueue        []int             // pending exchange indices for play-all (/play-all command)
	ttsAuto         bool              // auto-speak each response as it completes
	ttsRate         int               // words-per-minute for say(1) (default 200)
	ttsGen          int               // incremented on each startTTS; stale ttsDoneMsgs are ignored

	// Sentence-streaming TTS (active while ttsAuto and streaming)
	streamSentenceBuf  string            // tokens not yet emitted as a complete sentence
	ttsPendingSentences []ttsPendingItem // sentences waiting for current TTS to finish

	// command completion (active when input starts with /)
	completionItems []completionEntry // filtered list
	completionIdx   int               // highlighted row (-1 = none)

	// contextual parameter picker (active when input is "/cmd " with no arg yet)
	paramItems []string // candidate values (e.g. topic names, profile names)
	paramIdx   int      // highlighted row (-1 = none)

	// resource overlay (active when focus == paneResource)
	resourceLines    []string       // file content split into lines
	resourceName     string         // file name shown in top bar
	resourceCursor   int            // highlighted line index
	resourceScroll   viewport.Model // scrollable viewport for the overlay
	resourceTTSText  string         // text currently being spoken (for speed-change restart)
	resourceTTSQueue []int          // line indices still to be spoken (line-by-line playback)
	preFocus         focusPane      // focus state to restore on overlay close
	preFocusedExIdx  int            // focusedExIdx to restore on overlay close

	// topic picker overlay (active when focus == paneTopicPicker)
	topicPickerAll    []string
	topicPickerItems  []string
	topicPickerFilter string
	topicPickerIdx    int
	topicPickerScroll int

	// profile picker overlay (active when focus == paneProfilePicker)
	profilePickerAll    []string
	profilePickerItems  []string
	profilePickerFilter string
	profilePickerIdx    int
	profilePickerScroll int
}

// ttsPendingItem is a sentence queued for sentence-streaming TTS playback.
type ttsPendingItem struct {
	text  string
	exIdx int
}

// cmdResult holds one slash command invocation and its output.
type cmdResult struct {
	input           string
	output          []string
	warnLine        string   // if non-empty, rendered in red before output lines
	isError         bool
	quit            bool     // if true, the app should exit
	spokenText      string   // if non-empty, spoken via TTS when executed as a voice command
	suppressCmdPane bool     // if true, skip opening the cmd pane (e.g. resource overlay)
	execCmd         tea.Cmd  // if non-nil, run as a tea.Cmd after processing (e.g. ExecProcess)
}

// New creates a ready-to-run Model, loading existing history.
func New(eng *engine.Engine, cfg config.Config, dataDir string) Model {
	ta := textarea.New()
	ta.Placeholder = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.Focus()
	// SetWidth and Prompt are updated in syncInputPrompt() after layout is known.

	// Set textarea styles. Background is handled by the input pane wrapper
	// (raw ANSI), so all styles here use no background.
	noStyle := lipgloss.NewStyle()
	dimStyle := noStyle.Foreground(ActiveTheme.Dimmed)
	textStyle := noStyle.Foreground(ActiveTheme.TopBarText)
	promptStyle := noStyle.Foreground(ActiveTheme.InputPrompt)
	fullReset := textarea.Style{
		Base:             noStyle,
		CursorLine:       noStyle,
		CursorLineNumber: noStyle,
		EndOfBuffer:      noStyle,
		LineNumber:       dimStyle,
		Placeholder:      dimStyle,
		Prompt:           promptStyle,
		Text:             textStyle,
	}
	ta.FocusedStyle = fullReset
	ta.BlurredStyle = fullReset

	vp := viewport.New(0, 0)
	vp.Style = lipgloss.NewStyle()

	m := Model{
		eng:             eng,
		cfg:             cfg,
		dataDir:         dataDir,
		conv:            vp,
		input:           ta,
		focus:           paneInput,
		focusedExIdx:    -1,
		deletingExIdx:   -1,
		historyIdx:      -1,
		ttsExIdx:        -1,
		ttsRate:         200,
		cursorVisible:   true,
		windowFocused:   true,
		themeMode:       "auto",
		preFocusedExIdx: -1,
	}
	m.cmdScroll = viewport.New(0, 0)
	m.cmdScroll.Style = lipgloss.NewStyle() // no background
	m.loadUsageStats()
	m.loadHistory()
	return m
}

// Init starts the spinner ticker. Cursor blink is driven by spinnerTick.
func (m Model) Init() tea.Cmd {
	return tea.Batch(spinnerTick(), tea.EnableReportFocus)
}

func spinnerTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// isLongEntry returns true if the exchange exceeds the fold threshold.
func (m *Model) isLongEntry(ex exchange) bool {
	fl := m.foldLines
	if fl <= 0 {
		return false
	}
	userLong := strings.Count(ex.userMsg.Content, "\n") >= fl || len(ex.userMsg.Content) > 512
	asstLong := !ex.isNote && strings.Count(ex.asstMsg.Content, "\n") >= fl
	return userLong || asstLong
}

// loadHistory populates exchanges from the engine's current topic history.
func (m *Model) loadHistory() {
	h := m.eng.Topic().History
	for i := 0; i < len(h.Msgs); i++ {
		msg := h.Msgs[i]
		if msg.Role == core.RoleNote {
			m.exchanges = append(m.exchanges, exchange{
				userMsg:  msg,
				isNote:   true,
				complete: true,
				expanded: !m.foldOnStart,
			})
		} else if msg.Role == core.RoleUser && i+1 < len(h.Msgs) && h.Msgs[i+1].Role == core.RoleAssistant {
			asst := h.Msgs[i+1]
			ex := exchange{
				userMsg:  msg,
				asstMsg:  asst,
				model:    asst.Profile,
				complete: true,
				expanded: !m.foldOnStart,
			}
			if entry, ok := m.usageByTs[asst.Time.UnixNano()]; ok {
				ex.costUSD = entry.CostUSD
			}
			m.exchanges = append(m.exchanges, ex)
			i++
		}
	}
}

// loadUsageStats reads the usage log into topicStats, sessionStats, and usageByTs.
func (m *Model) loadUsageStats() {
	logPath := store.UsageLogPath(m.dataDir)
	entries, err := store.ReadUsageLog(logPath)
	if err != nil || len(entries) == 0 {
		return
	}
	agg := store.AggregateUsage(entries, m.eng.TopicName(), 0)
	m.topicStats = agg.Total
	aggAll := store.AggregateUsage(entries, "", 0)
	m.sessionStats = aggAll.Total

	// Build timestamp index for this topic so loadHistory can match exchanges.
	m.usageByTs = make(map[int64]store.UsageEntry)
	for _, e := range entries {
		if e.Topic == m.eng.TopicName() {
			m.usageByTs[e.Timestamp.UnixNano()] = e
		}
	}
}

// contextFillPct returns 0-100, or -1 if no limit is configured.
func (m *Model) contextFillPct() int {
	limit := m.eng.Profile().MaxContextTokens
	if limit <= 0 {
		return -1
	}
	used := 0
	for _, ex := range m.exchanges {
		used += core.ApproxTokens(ex.userMsg.Content)
		used += core.ApproxTokens(ex.asstMsg.Content)
	}
	pct := used * 100 / limit
	if pct > 100 {
		pct = 100
	}
	return pct
}

// inputPrompt returns the prefix shown in the input pane.
func (m *Model) inputPrompt() string {
	base := m.eng.TopicName() + "/" + m.eng.Profile().Model
	switch m.voiceState {
	case VoiceConversing:
		return base + "-chat> "
	case VoiceDictating:
		return base + "-note> "
	}
	return base + "> "
}

// inputVisualHeight returns the number of visual (wrapped) lines the input
// text occupies given the current terminal width, accounting for the prompt.
func (m *Model) inputVisualHeight() int {
	if m.width == 0 {
		return 1
	}
	prompt := m.inputPrompt()
	const padW = 1
	line0W := m.width - padW - len([]rune(prompt))
	contW := m.width - padW
	if line0W < 1 {
		line0W = 1
	}
	if contW < 1 {
		contW = 1
	}
	total := 0
	for i, line := range strings.Split(m.input.Value(), "\n") {
		runes := []rune(line)
		wW := contW
		if i == 0 {
			wW = line0W
		}
		if len(runes) == 0 {
			total++
		} else {
			total += (len(runes) + wW - 1) / wW
		}
	}
	if total < 1 {
		total = 1
	}
	if total > 5 {
		total = 5
	}
	return total
}

// syncInputPrompt updates the textarea's built-in Prompt field so the cursor
// appears on the same line as the prefix. Called on resize and profile/topic switch.
func (m *Model) syncInputPrompt() {
	prompt := m.inputPrompt()
	m.input.Prompt = prompt
	m.input.SetWidth(m.width - len([]rune(prompt)))
}

// cmdPaneHeight returns the height of the bottom pane in lines (excluding separator).
// Normal: 1 line (stats). Expanded: capped at 30% of terminal height.
func (m *Model) cmdPaneHeight() int {
	if len(m.completionItems) > 0 {
		return 1 + len(m.completionItems) // header + one row per match
	}
	if len(m.paramItems) > 0 {
		h := len(m.paramItems)
		max := m.height * 30 / 100
		if max < 3 {
			max = 3
		}
		if h > max {
			h = max
		}
		return h
	}
	if !m.cmdPaneOpen || m.lastCmd == nil {
		return 1
	}
	// 1 (header) + len(output lines), capped at 30% of terminal height.
	h := 1 + len(m.lastCmd.output)
	max := m.height * 30 / 100
	if max < 3 {
		max = 3
	}
	if h > max {
		h = max
	}
	return h
}

// syncLayout recalculates the conversation viewport height based on current
// terminal size and textarea height. Call after resize or textarea height change.
// pickerOverlayHeight returns the fixed line count used by both pickers.
// 1 (title) + 1 (sep) + maxVisible (items) + 1 (sep) + 1 (count) + 1 (keys)
func pickerOverlayHeight() int { return topicPickerMaxVisible + 5 }

func (m *Model) syncLayout() {
	// Layout (each value = number of terminal lines):
	//   top bar:    2 (text + separator)
	//   conv:       convH
	//   input pane: 1 (separator) + textarea.Height()  (hidden when picker open)
	//   bottom pane: 1 (separator) + cmdPaneHeight()    (hidden when picker open)
	//   picker:     pickerOverlayHeight()               (only when picker open)
	pickerOpen := m.focus == paneTopicPicker || m.focus == paneProfilePicker
	inputH := m.input.Height() + 1
	bottomH := 1 + m.cmdPaneHeight()
	var convH int
	if pickerOpen {
		convH = m.height - 2 - pickerOverlayHeight()
	} else {
		convH = m.height - 2 - inputH - bottomH
	}
	if convH < 3 {
		convH = 3
	}
	m.conv.Width = m.width
	m.conv.Height = convH
	m.cmdScroll.Width = m.width
	m.cmdScroll.Height = m.cmdPaneHeight()

	// Resource overlay: full height minus top bar (2) and hint bar (2).
	resourceH := m.height - 4
	if resourceH < 1 {
		resourceH = 1
	}
	m.resourceScroll.Width = m.width
	m.resourceScroll.Height = resourceH
}

// rebuildConvContent re-renders all exchanges into the viewport.
// When paneConv has focus and an exchange is selected, scrolls to show it.
// Otherwise scrolls to the bottom only when the user hasn't manually scrolled up.
func (m *Model) rebuildConvContent() {
	content, offsets := renderConversation(m)
	m.conv.SetContent(content)
	if m.focus == paneConv && m.focusedExIdx >= 0 && m.focusedExIdx < len(offsets) {
		m.conv.SetYOffset(offsets[m.focusedExIdx])
	} else if !m.userScrolled && m.ttsExIdx >= 0 && m.ttsExIdx < len(offsets) {
		m.conv.SetYOffset(offsets[m.ttsExIdx])
	} else if !m.userScrolled {
		m.conv.GotoBottom()
	}
}

// pushHistory appends val to inputHistory, deduplicating consecutive identical
// entries and capping at 128. Resets historyIdx to -1.
// Entries longer than 64 runes are truncated to 60 + " ...".
func (m *Model) pushHistory(val string) {
	if val == "" {
		return
	}
	entry := val
	if runes := []rune(val); len(runes) > 64 {
		entry = string(runes[:60]) + " ..."
	}
	if len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != entry {
		m.inputHistory = append(m.inputHistory, entry)
		if len(m.inputHistory) > 128 {
			m.inputHistory = m.inputHistory[len(m.inputHistory)-128:]
		}
	}
	m.historyIdx = -1
	m.historySaved = ""
}

// isTTSPlaying reports whether any TTS backend is currently active.
func (m *Model) isTTSPlaying() bool {
	return m.ttsCmd != nil || m.ttsKokoroStop != nil
}

// killTTS stops any in-flight TTS (say or Kokoro) and clears all TTS state.
func (m *Model) killTTS() {
	m.ttsExIdx = -1
	m.ttsQueue = nil
	m.ttsPendingSentences = nil
	m.streamSentenceBuf = ""
	m.resourceTTSQueue = nil
	setTTSMicMute(false)
	if m.ttsCmd != nil {
		cmd := m.ttsCmd
		m.ttsCmd = nil
		// Kill the entire process group (say forks a child for audio synthesis).
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	if m.ttsKokoroStop != nil {
		close(m.ttsKokoroStop)
		m.ttsKokoroStop = nil
	}
}

// killTTSAudio stops only the audio process/goroutine without clearing any queues.
// Used when restarting the current line at a new speed ([/] in resource view).
func killTTSAudio(m *Model) {
	if m.ttsCmd != nil {
		cmd := m.ttsCmd
		m.ttsCmd = nil
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	if m.ttsKokoroStop != nil {
		close(m.ttsKokoroStop)
		m.ttsKokoroStop = nil
	}
}

// suspendTTS stops audio playback but saves the pending sentence queue and
// stream buffer so that resumeTTS can restore them. Unlike killTTS it does
// not discard the queue.
func (m *Model) suspendTTS() {
	m.suspended = &suspendedTTS{
		sentences: m.ttsPendingSentences,
		streamBuf: m.streamSentenceBuf,
	}
	m.ttsPendingSentences = nil
	m.streamSentenceBuf = ""
	// Stop audio output without discarding the queue (already moved above).
	m.ttsExIdx = -1
	if m.ttsCmd != nil {
		cmd := m.ttsCmd
		m.ttsCmd = nil
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	if m.ttsKokoroStop != nil {
		close(m.ttsKokoroStop)
		m.ttsKokoroStop = nil
	}
}

// resumeTTS restores a previously suspended TTS queue and restarts playback.
// Does nothing if there was no suspension.
func (m *Model) resumeTTS() {
	if m.suspended == nil {
		return
	}
	m.ttsPendingSentences = m.suspended.sentences
	m.streamSentenceBuf = m.suspended.streamBuf
	m.suspended = nil
	// Drain the restored queue: start playing the first pending sentence.
	if len(m.ttsPendingSentences) > 0 {
		next := m.ttsPendingSentences[0]
		m.ttsPendingSentences = m.ttsPendingSentences[1:]
		m.ttsExIdx = next.exIdx
		m.ttsGen++
		startTTS(next.text, next.exIdx, m)
	}
}

// scrollToBottom forces the viewport to the bottom and clears the userScrolled flag.
func (m *Model) scrollToBottom() {
	m.userScrolled = false
	m.conv.GotoBottom()
}

// rebuildResourceContent re-renders the resource overlay lines into the viewport.
func (m *Model) rebuildResourceContent() {
	if len(m.resourceLines) == 0 {
		return
	}
	m.resourceScroll.SetContent(renderResourceLines(m))
}

// scrollResourceToCursor scrolls the resource viewport so the cursor line is visible.
func (m *Model) scrollResourceToCursor() {
	vp := &m.resourceScroll
	if m.resourceCursor < vp.YOffset {
		vp.SetYOffset(m.resourceCursor)
	} else if m.resourceCursor >= vp.YOffset+vp.Height {
		vp.SetYOffset(m.resourceCursor - vp.Height + 1)
	}
}

// closeResourceOverlay tears down the resource overlay and restores previous focus.
func (m *Model) closeResourceOverlay() {
	m.setFocus(m.preFocus)
	m.focusedExIdx = m.preFocusedExIdx
	m.resourceLines = nil
	m.resourceName = ""
	m.resourceCursor = 0
	m.resourceTTSText = ""
	m.resourceTTSQueue = nil
	if m.preFocus == paneInput {
		m.input.Focus()
	}
	m.rebuildConvContent()
	m.syncLayout()
}

// openTopicPicker initialises and opens the topic picker overlay.
func (m *Model) openTopicPicker() {
	topics, err := m.eng.ListTopics()
	if err != nil {
		return
	}
	current := m.eng.TopicName()
	var all []string
	all = append(all, current)
	for _, t := range topics {
		if t != current {
			all = append(all, t)
		}
	}
	m.topicPickerAll = all
	m.topicPickerFilter = ""
	m.topicPickerItems = all
	m.topicPickerIdx = 0
	m.topicPickerScroll = 0
	m.preFocus = m.focus
	m.preFocusedExIdx = m.focusedExIdx
	m.setFocus(paneTopicPicker)
	m.syncLayout()
	m.rebuildConvContent()
}

// closeTopicPicker tears down the topic picker and restores previous focus.
func (m *Model) closeTopicPicker() {
	m.setFocus(m.preFocus)
	m.focusedExIdx = m.preFocusedExIdx
	m.topicPickerAll = nil
	m.topicPickerItems = nil
	m.topicPickerFilter = ""
	m.topicPickerIdx = 0
	m.topicPickerScroll = 0
	if m.preFocus == paneInput {
		m.input.Focus()
	}
	m.syncLayout()
	m.rebuildConvContent()
}

// filterTopicPicker re-filters topicPickerItems from topicPickerAll using the current filter.
func (m *Model) filterTopicPicker() {
	f := strings.ToLower(m.topicPickerFilter)
	if f == "" {
		m.topicPickerItems = m.topicPickerAll
	} else {
		var out []string
		for _, t := range m.topicPickerAll {
			if strings.Contains(strings.ToLower(t), f) {
				out = append(out, t)
			}
		}
		m.topicPickerItems = out
	}
	m.topicPickerIdx = 0
	m.topicPickerScroll = 0
}

// openProfilePicker initialises and opens the profile picker overlay.
func (m *Model) openProfilePicker() {
	current := m.eng.ProfileCode()
	names := make([]string, 0, len(m.cfg.Profiles))
	for name := range m.cfg.Profiles {
		names = append(names, name)
	}
	sortedNames := make([]string, 0, len(names))
	sortedNames = append(sortedNames, current)
	for _, n := range names {
		if n != current {
			sortedNames = append(sortedNames, n)
		}
	}
	m.profilePickerAll = sortedNames
	m.profilePickerFilter = ""
	m.profilePickerItems = sortedNames
	m.profilePickerIdx = 0
	m.profilePickerScroll = 0
	m.preFocus = m.focus
	m.preFocusedExIdx = m.focusedExIdx
	m.setFocus(paneProfilePicker)
	m.syncLayout()
	m.rebuildConvContent()
}

// closeProfilePicker tears down the profile picker and restores previous focus.
func (m *Model) closeProfilePicker() {
	m.setFocus(m.preFocus)
	m.focusedExIdx = m.preFocusedExIdx
	m.profilePickerAll = nil
	m.profilePickerItems = nil
	m.profilePickerFilter = ""
	m.profilePickerIdx = 0
	m.profilePickerScroll = 0
	if m.preFocus == paneInput {
		m.input.Focus()
	}
	m.syncLayout()
	m.rebuildConvContent()
}

// filterProfilePicker re-filters profilePickerItems from profilePickerAll using the current filter.
func (m *Model) filterProfilePicker() {
	f := strings.ToLower(m.profilePickerFilter)
	if f == "" {
		m.profilePickerItems = m.profilePickerAll
	} else {
		var out []string
		for _, n := range m.profilePickerAll {
			if strings.Contains(strings.ToLower(n), f) {
				out = append(out, n)
			}
		}
		m.profilePickerItems = out
	}
	m.profilePickerIdx = 0
	m.profilePickerScroll = 0
}
