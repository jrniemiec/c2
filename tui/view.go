package tui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jrniemiec/c2/config"
)

var multiBlankRE = regexp.MustCompile(`\n{2,}`)

// compactLines collapses multiple consecutive blank lines into a single newline.
func compactLines(s string) string {
	return multiBlankRE.ReplaceAllString(strings.TrimRight(s, "\n"), "\n")
}

// wordWrap wraps plain text (no ANSI) at the given column width, breaking at
// word boundaries. Each existing newline starts a fresh line.
func wordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	var out strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}
		col := 0
		for j, w := range words {
			wlen := len([]rune(w))
			if j == 0 {
				out.WriteString(w)
				col = wlen
			} else if col+1+wlen > width {
				out.WriteByte('\n')
				out.WriteString(w)
				col = wlen
			} else {
				out.WriteByte(' ')
				out.WriteString(w)
				col += 1 + wlen
			}
		}
	}
	return out.String()
}

// View renders the full TUI.
func (m Model) View() string {
	if m.width == 0 {
		return ""
	}
	if m.focus == paneResource {
		return strings.Join([]string{
			renderResourceTopBar(&m),
			m.resourceScroll.View(),
			renderResourceHintBar(&m),
		}, "\n")
	}
	if m.focus == paneTopicPicker {
		return strings.Join([]string{
			renderTopBar(&m),
			m.conv.View(),
			renderTopicPicker(&m),
		}, "\n")
	}
	if m.focus == paneProfilePicker {
		return strings.Join([]string{
			renderTopBar(&m),
			m.conv.View(),
			renderProfilePicker(&m),
		}, "\n")
	}
	return strings.Join([]string{
		renderTopBar(&m),
		m.conv.View(),
		renderInputPane(&m),
		renderStatusBar(&m),
	}, "\n")
}

// =============================================================================
// Raw ANSI helpers — foreground color only, zero background side-effects.
// lipgloss is only used for the box border (structural, not color).
// =============================================================================

// fg wraps text in a truecolor foreground sequence and resets all attributes
// afterward. The reset (\033[0m) clears any stale background from bubbles.
func fg(col lipgloss.Color, text string) string {
	s := string(col)
	if r, g, b, ok := hexToRGB(s); ok {
		return fmt.Sprintf("\033[38;2;%d;%d;%dm%s\033[0m", r, g, b, text)
	}
	// ANSI 256-color index (e.g. "241")
	if n, err := strconv.Atoi(s); err == nil {
		return fmt.Sprintf("\033[38;5;%dm%s\033[0m", n, text)
	}
	return text
}

func fgBold(col lipgloss.Color, text string) string {
	s := string(col)
	if r, g, b, ok := hexToRGB(s); ok {
		return fmt.Sprintf("\033[1;38;2;%d;%d;%dm%s\033[0m", r, g, b, text)
	}
	if n, err := strconv.Atoi(s); err == nil {
		return fmt.Sprintf("\033[1;38;5;%dm%s\033[0m", n, text)
	}
	return "\033[1m" + text + "\033[0m"
}

// fgShell renders text in bright bold red when the first logical line starts
// with '!', otherwise falls back to normal InputText color.
func fgShell(t Theme, firstLine, text string) string {
	if strings.HasPrefix(firstLine, "!") {
		// bright bold red: ESC[1;91m
		return "\033[1;91m" + text + "\033[0m"
	}
	return fg(t.InputText, text)
}

// lerpColor interpolates between c1 (t=0) and c2 (t=1).
func lerpColor(c1, c2 lipgloss.Color, t float64) lipgloss.Color {
	r1, g1, b1, ok1 := hexToRGB(string(c1))
	r2, g2, b2, ok2 := hexToRGB(string(c2))
	if !ok1 || !ok2 {
		return c2
	}
	lerp := func(a, b int64) int64 { return a + int64(t*float64(b-a)) }
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", lerp(r1, r2), lerp(g1, g2), lerp(b1, b2)))
}

// waveDots renders N dots with a brightness wave: the peak travels left-to-right.
// peak = which dot index is brightest (0-based), wraps around.
func waveDots(n, peak int, bright, dim lipgloss.Color) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		// Distance from peak, wrapping around.
		dist := i - peak
		if dist < 0 {
			dist = -dist
		}
		if wrap := n - dist; wrap < dist {
			dist = wrap
		}
		// t=1 at peak, falls off toward 0 at max distance.
		t := 1.0 - float64(dist)/float64((n+1)/2+1)
		if t < 0 {
			t = 0
		}
		col := lerpColor(dim, bright, t)
		sb.WriteString(fg(col, "●"))
	}
	return sb.String()
}

func fgFaint(col lipgloss.Color, text string) string {
	s := string(col)
	if r, g, b, ok := hexToRGB(s); ok {
		return fmt.Sprintf("\033[2;38;2;%d;%d;%dm%s\033[0m", r, g, b, text)
	}
	if n, err := strconv.Atoi(s); err == nil {
		return fmt.Sprintf("\033[2;38;5;%dm%s\033[0m", n, text)
	}
	return "\033[2m" + text + "\033[0m"
}

func hexToRGB(hex string) (r, g, b int64, ok bool) {
	if len(hex) != 7 || hex[0] != '#' {
		return 0, 0, 0, false
	}
	r, err := strconv.ParseInt(hex[1:3], 16, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	g, err = strconv.ParseInt(hex[3:5], 16, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	b, err = strconv.ParseInt(hex[5:7], 16, 64)
	if err != nil {
		return 0, 0, 0, false
	}
	return r, g, b, true
}

// visibleWidth returns the display width of a string ignoring ANSI escape codes.
func visibleWidth(s string) int {
	return lipgloss.Width(s)
}

// fgLines applies fg() to each \n-separated line individually so every line
// carries its own complete ANSI open/close sequence. This prevents viewport
// line-splitting and lipgloss re-wrapping from breaking multi-line colored text.
func fgLines(col lipgloss.Color, text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = fg(col, l)
	}
	return strings.Join(lines, "\n")
}

// =============================================================================
// Top bar
// =============================================================================

func renderTopBar(m *Model) string {
	t := ActiveTheme

	const pad = 1

	left := fgBold(t.TopBarText, "c2")
	barSep := fg(t.Dimmed, " │ ")

	// Center: topic · [context capacity ·] model
	pct := m.contextFillPct()
	var ctxPart string
	if pct > 0 {
		var fillStr string
		if pct >= 90 {
			fillStr = fgBold(t.ContextWarning, fmt.Sprintf("%d%%", pct))
		} else {
			fillStr = fg(t.TopBarText, fmt.Sprintf("%d%%", pct))
		}
		ctxPart = fg(t.TopBarText, " · context: ") + fillStr
	}
	modelPart := fg(t.TopBarText, " · model: "+m.eng.Profile().Model)
	if m.ttsAuto {
		modelPart += fg(t.StreamingText, " ♪")
	}
	center := fg(t.TopBarText, "topic: "+m.eng.TopicName()) +
		ctxPart +
		modelPart

	// Right: nav/scroll indicator, then mode toggle indicator.
	var navPart string
	if m.focus == paneConv {
		if m.focusedExIdx >= 0 {
			navPart = fgBold(t.InputPrompt, fmt.Sprintf("[ #%d ]", m.focusedExIdx+1))
		} else {
			navPart = fgBold(t.InputPrompt, "[ nav ]")
		}
	} else if m.userScrolled {
		total := m.conv.TotalLineCount()
		if total > 0 {
			scrollPct := (m.conv.YOffset * 100) / total
			var arrow string
			switch {
			case scrollPct < 40:
				arrow = "↓"
			case scrollPct >= 60:
				arrow = "↑"
			default:
				arrow = "↕"
			}
			navPart = fg(t.InputPrompt, fmt.Sprintf("%s %d%%", arrow, scrollPct))
		}
	}

	// Mode indicator: pulsating red ● for voice, plain ○ for text.
	// Show pipeline error inline if present.
	var modeIndicator string
	if m.voiceErr != "" && m.mode == modeVoice {
		modeIndicator = fg(lipgloss.Color("#FF4444"), "● ERR: "+m.voiceErr)
	} else if m.mode == modeVoice {
		var stateLabel string
		switch m.voiceState {
		case VoiceIdle:
			stateLabel = " VOICE MODE: IDLE      "
		case VoiceAwake:
			stateLabel = " VOICE MODE: AWAKE     "
		case VoiceDictating:
			stateLabel = " VOICE MODE: DICTATING "
		case VoiceConversing:
			stateLabel = " VOICE MODE: CONVERSING"
		case VoiceExecuting:
			stateLabel = " VOICE MODE: EXECUTING "
		default:
			stateLabel = " VOICE MODE            "
		}
		levelBar := renderMicLevel(m, t)
		modeIndicator = levelBar + fg(t.TopBarText, stateLabel)
	} else {
		modeIndicator = fg(t.TopBarText, "○ TEXT MODE")
	}

	var right string
	if navPart != "" {
		right = navPart + "  " + modeIndicator
	} else {
		right = modeIndicator
	}

	leftCenter := strings.Repeat(" ", pad) + left + barSep + center
	gap := m.width - visibleWidth(leftCenter) - visibleWidth(right) - pad
	if gap < 1 {
		gap = 1
	}
	bar := leftCenter + strings.Repeat(" ", gap) + right + strings.Repeat(" ", pad)
	return bar + "\n" + fg(t.BoxBorder, strings.Repeat("─", m.width))
}

// renderMicLevel renders a 4-character symmetric mountain bar driven by peak-hold decay.
// Shape: far(0.3) · near(0.8) · near(0.8) · far(0.3) of the current peak.
// Colour: t.StreamingText above speech threshold, t.Dimmed below.
func renderMicLevel(m *Model, t Theme) string {
	const blocks = "⣀⣄⣤⣦⣶⣷⣾⣿"
	const speechThreshold = 0.01
	runes := []rune(blocks)

	levelToBlock := func(v float32) string {
		if v < 0.001 {
			return string(runes[0:1])
		}
		scaled := math.Log(float64(v)/0.001) / math.Log(0.3/0.001)
		if scaled > 1 {
			scaled = 1
		}
		idx := int(scaled * float64(len(runes)-1))
		return string(runes[idx : idx+1])
	}

	levels := [4]float32{
		m.voicePeakOuter * 0.3,
		m.voicePeakInner * 0.8,
		m.voicePeakInner * 0.8,
		m.voicePeakOuter * 0.3,
	}
	var out strings.Builder
	for _, v := range levels {
		ch := levelToBlock(v)
		if v >= speechThreshold {
			out.WriteString(fg(t.StreamingText, ch))
		} else {
			out.WriteString(fg(t.Dimmed, ch))
		}
	}
	return out.String()
}

// =============================================================================
// Conversation pane
// =============================================================================

// renderConversation builds the full conversation string written into the viewport.
// It also returns the starting line offset for each exchange, used for scroll-to-focus.
func renderConversation(m *Model) (string, []int) {
	t := ActiveTheme

	// Box style is the one place we use lipgloss — structural border only,
	// no background color set.
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.BoxBorder).
		Width(m.width - 4)
	boxStyleRed := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#FF6B6B")).
		Width(m.width - 4)

	parts := make([]string, len(m.exchanges))

	for i, ex := range m.exchanges {
		focused := m.focus == paneConv && m.focusedExIdx == i
		deleting := (m.pendingAction != nil && m.focusedExIdx == i) || m.deletingExIdx == i

		// Layout: col 1 = bullet (●), col 2 = space, col 3+ = text.
		// Both user and assistant text start at column 3 (2-char prefix).
		const prefixW = 2
		wrapWidth := m.width - prefixW

		// addPrefix prepends a 2-char prefix to each line.
		// firstPrefix is used on line 0, restPrefix on continuation lines.
		addPrefix := func(s, firstPrefix, restPrefix string) string {
			lines := strings.Split(s, "\n")
			for i, l := range lines {
				if i == 0 {
					lines[i] = firstPrefix + l
				} else {
					lines[i] = restPrefix + l
				}
			}
			return strings.Join(lines, "\n")
		}

		fl := m.foldLines
		if fl <= 0 {
			fl = 0 // 0 means never fold — handled by never triggering the threshold
		}
		var turnContent string
		if ex.isNote {
			noteText := ex.userMsg.Content
			noteLines := strings.Split(noteText, "\n")
			if fl > 0 && (len(noteLines) > fl || len(noteText) > 512) && !ex.expanded {
				cap := fl
				if cap > len(noteLines) {
					cap = len(noteLines)
				}
				noteText = strings.Join(noteLines[:cap], "\n")
				noteText += "\n" + fg(t.Dimmed, fmt.Sprintf("... (%d more lines)", len(noteLines)-cap))
			}
			noteRaw := wordWrap(compactLines(noteText), wrapWidth)
			turnContent = addPrefix(fgLines(t.UserText, noteRaw),
				fg(t.UserText, "📌 "),
				"  ")
		} else {
			userText := ex.userMsg.Content
			userLines := strings.Split(userText, "\n")
			if fl > 0 && (len(userLines) > fl || len(userText) > 512) && !ex.expanded {
				cap := fl
				if cap > len(userLines) {
					cap = len(userLines)
				}
				userText = strings.Join(userLines[:cap], "\n")
				userText += "\n" + fg(t.Dimmed, fmt.Sprintf("... (%d more lines)", len(userLines)-cap))
			}
			userRaw := wordWrap(compactLines(userText), wrapWidth)
			var userContent string
			if m.chatLabels {
				userContent = addPrefix(fgLines(t.UserText, userRaw),
					fgBold(t.UserText, "[you]: "),
					"  ")
			} else {
				userContent = addPrefix(fgLines(t.UserText, userRaw),
					fg(t.AssistantText, "● "),
					"  ")
			}

			var asstRaw string
			if ex.complete {
				asstText := ex.asstMsg.Content
				asstLines := strings.Split(asstText, "\n")
				if fl > 0 && len(asstLines) > fl && !ex.expanded {
					asstText = strings.Join(asstLines[:fl], "\n")
					asstText += "\n" + fg(t.Dimmed, fmt.Sprintf("... (%d more lines)", len(asstLines)-fl))
				}
				asstRaw = wordWrap(compactLines(asstText), wrapWidth)
			} else {
				asstRaw = wordWrap(compactLines(m.streamBuf), wrapWidth)
			}
			var asstContent string
			if m.chatLabels {
				profile := ex.model
				if profile == "" {
					profile = m.eng.ProfileCode()
				}
				asstContent = addPrefix(fgLines(t.AssistantText, asstRaw),
					fgBold(t.AssistantText, "["+profile+"]: "),
					"  ")
			} else {
				asstContent = addPrefix(fgLines(t.AssistantText, asstRaw), "  ", "  ")
			}
			turnContent = userContent + "\n" + asstContent
		}

		speaking := m.ttsExIdx == i
		if focused || deleting || speaking {
			left := fg(t.Dimmed, ex.userMsg.Time.Format("15:04"))
			if ex.model != "" {
				left += fg(t.Dimmed, "  "+ex.model)
			}
			if speaking {
				left += fgBold(t.StreamingText, "  ♪")
			}
			// Right-aligned key hints (shown when focused, not deleting).
			var header string
			if focused && !deleting {
				expandHint := "v expand"
				if ex.expanded {
					expandHint = "v collapse"
				}
				hints := fg(t.Dimmed, expandHint+" · s speak · x delete")
				innerW := m.width - 4
				leftW := visibleWidth(left)
				hintsW := visibleWidth(hints)
				pad := innerW - leftW - hintsW
				if pad < 1 {
					pad = 1
				}
				header = left + strings.Repeat(" ", pad) + hints
			} else {
				header = left
			}

			if deleting {
				turnContent = boxStyleRed.Render(header + "\n" + turnContent)
			} else {
				turnContent = boxStyle.Render(header + "\n" + turnContent)
			}
		}

		parts[i] = turnContent
	}

	// Compute starting line offset for each exchange.
	// Exchanges are joined by "\n\n": the first "\n" ends the last line of the
	// previous exchange and the second "\n" inserts one blank separator line.
	// So each exchange starts at: offset[i] = offset[i-1] + lineCount(parts[i-1]) + 1
	// where lineCount(s) = strings.Count(s, "\n") + 1.
	offsets := make([]int, len(parts))
	lineOffset := 0
	for i, part := range parts {
		offsets[i] = lineOffset
		if i < len(parts)-1 {
			lineOffset += strings.Count(part, "\n") + 2 // lines in part + 1 blank separator
		}
	}

	return strings.Join(parts, "\n\n"), offsets
}

const waveDotCount = 5

func renderSpinner(m *Model) string {
	t := ActiveTheme
	if m.spinnerFrame%2 == 0 {
		return fgBold(t.Spinner, "❄")
	}
	return fgFaint(t.Spinner, "❄")
}

func renderWaveIndicator(frame int, label string, bright, dim lipgloss.Color) string {
	// Build the full rune slice: "❄ <label> ●●●●●"
	full := "❄ " + label + " "
	runes := []rune(full)
	dots := []rune("●●●●●")
	all := append(runes, dots...)
	n := len(all)

	// Wave peak advances every tick (~100ms), travels across full string.
	peak := frame % n

	var sb strings.Builder
	for i, r := range all {
		dist := i - peak
		if dist < 0 {
			dist = -dist
		}
		if wrap := n - dist; wrap < dist {
			dist = wrap
		}
		t := 1.0 - float64(dist)/float64(n/2+1)
		if t < 0 {
			t = 0
		}
		col := lerpColor(dim, bright, t)
		sb.WriteString(fg(col, string(r)))
	}
	return sb.String()
}

// =============================================================================
// Input pane
// =============================================================================

// renderInputPane renders the prompt + text entirely with raw ANSI — never
// calling m.input.View(). This guarantees no background color sequences are
// emitted (termenv always wraps with \033[m...\033[0m which resets the
// background, potentially differing from the terminal's configured background).
//
// The textarea model is kept for editing state and cursor position only.
// shellBorderColor returns bright bold red ANSI if input starts with '!', empty otherwise.
// Used to tint both separator lines surrounding the input pane.
const shellBorderColor = "\033[1;91m"

func shellSep(m *Model, fallback string) string {
	if strings.HasPrefix(m.input.Value(), "!") {
		return shellBorderColor + strings.Repeat("─", m.width) + "\033[0m"
	}
	return fallback
}

func renderInputPane(m *Model) string {
	t := ActiveTheme
	var sep string
	if m.focus == paneConv {
		sep = shellSep(m, fg(t.InputPrompt, strings.Repeat("─", m.width)))
	} else {
		sep = shellSep(m, fg(t.BoxBorder, strings.Repeat("─", m.width)))
	}

	prompt := m.inputPrompt()
	promptRunes := []rune(prompt)
	const padW = 1
	shellMode := strings.HasPrefix(m.input.Value(), "!")
	var line0W, contW int
	if shellMode {
		line0W = m.width
		contW = m.width
	} else {
		line0W = m.width - padW - len(promptRunes)
		contW = m.width - padW
	}
	if line0W < 1 {
		line0W = 1
	}
	if contW < 1 {
		contW = 1
	}

	curLogLine := m.input.Line()
	curLineInfo := m.input.LineInfo()
	curLogCol := curLineInfo.StartColumn + curLineInfo.ColumnOffset

	logicalLines := strings.Split(m.input.Value(), "\n")
	if len(logicalLines) == 0 {
		logicalLines = []string{""}
	}

	var rendered []string
	firstVisualLine := true

	for li, line := range logicalLines {
		runes := []rune(line)
		wW := contW
		if li == 0 {
			wW = line0W
		}

		// Split logical line into visual chunks.
		type chunk struct {
			runes      []rune
			logStart   int // column offset within logical line
		}
		var chunks []chunk
		if len(runes) == 0 {
			chunks = []chunk{{runes: []rune{}, logStart: 0}}
		} else {
			for start := 0; start < len(runes); start += wW {
				end := start + wW
				if end > len(runes) {
					end = len(runes)
				}
				chunks = append(chunks, chunk{runes: runes[start:end], logStart: start})
			}
		}

		for ci, ch := range chunks {
			// Build prefix.
			var prefix string
			if firstVisualLine {
				promptColor := t.InputPrompt
				if m.voiceState == VoiceConversing ||
					(m.voiceState == VoiceDictating && m.pendingDictCmd == "note") {
					// Pulse between Dimmed and TopBarText on a 2s cosine cycle.
					const period = 40 // frames at 100ms each = 4s
					phase := float64(m.spinnerFrame%period) / float64(period)
					pulse := (1 - math.Cos(phase*2*math.Pi)) / 2
					promptColor = lerpColor(t.Dimmed, t.TopBarText, pulse)
				}
				if shellMode {
					prefix = ""
				} else {
					prefix = strings.Repeat(" ", padW) + fg(promptColor, prompt)
				}
				firstVisualLine = false
			} else {
				if shellMode {
					prefix = ""
				} else {
					prefix = strings.Repeat(" ", padW)
				}
			}

			// Is cursor in this chunk?
			if li == curLogLine && m.focus == paneInput {
				chunkEnd := ch.logStart + len(ch.runes)
				isLast := ci == len(chunks)-1
				if curLogCol >= ch.logStart && (curLogCol < chunkEnd || isLast) {
					colInChunk := curLogCol - ch.logStart
					if colInChunk > len(ch.runes) {
						colInChunk = len(ch.runes)
					}
					before := string(ch.runes[:colInChunk])
					var curChar, after string
					if colInChunk < len(ch.runes) {
						curChar = string(ch.runes[colInChunk])
						after = string(ch.runes[colInChunk+1:])
					} else {
						curChar = " "
					}
					var cursorSeq string
					if m.cursorVisible {
						cursorSeq = "\033[7m" + curChar + "\033[27m"
					} else {
						cursorSeq = fg(t.InputText, curChar)
						if curChar == " " {
							cursorSeq = " "
						}
					}
					rendered = append(rendered,
						prefix+fgShell(t, logicalLines[0], before)+cursorSeq+fgShell(t, logicalLines[0], after))
					continue
				}
			}
			rendered = append(rendered, prefix+fgShell(t, logicalLines[0], string(ch.runes)))
		}
	}

	return sep + "\n" + strings.Join(rendered, "\n")
}

// =============================================================================
// Status bar
// =============================================================================

// renderCmdOutput builds the scrollable content for the command pane.
func renderCmdOutput(m *Model) string {
	if m.lastCmd == nil {
		return ""
	}
	t := ActiveTheme
	r := m.lastCmd
	var dot string
	if r.isError {
		dot = fg("#FF6B6B", "●")
	} else {
		dot = fg("#A3BE8C", "●")
	}
	header := dot + " " + fg(t.Dimmed, r.input)
	var sb strings.Builder
	sb.WriteString(header)
	if r.warnLine != "" {
		// Render warning and first output line together on one line.
		sb.WriteByte('\n')
		first := ""
		startIdx := 0
		if len(r.output) > 0 {
			first = "  " + fg(t.AssistantText, r.output[0])
			startIdx = 1
		}
		sb.WriteString("  " + fg("#FF6B6B", r.warnLine) + first)
		for _, line := range r.output[startIdx:] {
			sb.WriteByte('\n')
			sb.WriteString("  " + fg(t.AssistantText, line))
		}
	} else {
		for _, line := range r.output {
			sb.WriteByte('\n')
			if r.isError {
				sb.WriteString("  " + fg("#FF6B6B", line))
			} else {
				sb.WriteString("  " + fg(t.AssistantText, line))
			}
		}
	}
	// Inline cursor after the last output line when a destructive action is pending.
	if m.pendingAction != nil {
		sb.WriteString(" " + fg(t.InputText, m.confirmBuf) + "\033[4m \033[24m")
	}
	// Half-line padding at the bottom.
	sb.WriteString("\n")
	return sb.String()
}

func renderCompletionPane(m *Model) string {
	t := ActiveTheme
	var sb strings.Builder
	// Find longest command for alignment.
	maxCmd := 0
	for _, e := range m.completionItems {
		if len(e.cmd) > maxCmd {
			maxCmd = len(e.cmd)
		}
	}
	for i, e := range m.completionItems {
		cmdPart := fmt.Sprintf(" %-*s  ", maxCmd, e.cmd)
		var line string
		if i == m.completionIdx {
			line = fg(t.TopBarText, cmdPart+e.desc)
		} else {
			line = fg(t.InputText, cmdPart) + fg(t.Dimmed, e.desc)
		}
		sb.WriteByte('\n')
		sb.WriteString(line)
	}
	return sb.String()
}

func renderParamPane(m *Model) string {
	t := ActiveTheme
	var sb strings.Builder
	for i, item := range m.paramItems {
		var line string
		if i == m.paramIdx {
			line = fg(t.TopBarText, " "+item)
		} else {
			line = fg(t.InputText, " "+item)
		}
		sb.WriteByte('\n')
		sb.WriteString(line)
	}
	return sb.String()
}

func renderBottomPane(m *Model) string {
	t := ActiveTheme
	sep := shellSep(m, fg(t.BoxBorder, strings.Repeat("─", m.width)))
	if len(m.paramItems) > 0 {
		return sep + renderParamPane(m)
	}
	if len(m.completionItems) > 0 {
		return sep + renderCompletionPane(m)
	}
	if m.cmdPaneOpen && m.lastCmd != nil {
		return sep + "\n" + m.cmdScroll.View()
	}
	return renderStatsLine(m, sep)
}

func renderStatusBar(m *Model) string {
	return renderBottomPane(m)
}

// =============================================================================
// Resource overlay
// =============================================================================

// renderResourceLines builds the scrollable content for the resource overlay.
// The cursor line is highlighted with a ▶ marker.
func renderResourceLines(m *Model) string {
	t := ActiveTheme
	var sb strings.Builder
	for i, line := range m.resourceLines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if i == m.resourceCursor {
			sb.WriteString(fgBold(t.InputPrompt, "▶ ") + fg(t.TopBarText, line))
		} else {
			sb.WriteString(fg(t.Dimmed, "  ") + fg(t.AssistantText, line))
		}
	}
	return sb.String()
}

func renderResourceTopBar(m *Model) string {
	t := ActiveTheme
	const pad = 1
	left := fgBold(t.TopBarText, "c2")
	barSep := fg(t.Dimmed, " │ ")
	center := fg(t.TopBarText, "resource: "+m.resourceName)
	total := len(m.resourceLines)
	right := fg(t.Dimmed, fmt.Sprintf("line %d / %d", m.resourceCursor+1, total))
	leftCenter := strings.Repeat(" ", pad) + left + barSep + center
	gap := m.width - visibleWidth(leftCenter) - visibleWidth(right) - pad
	if gap < 1 {
		gap = 1
	}
	bar := leftCenter + strings.Repeat(" ", gap) + right + strings.Repeat(" ", pad)
	return bar + "\n" + fg(t.BoxBorder, strings.Repeat("─", m.width))
}

func renderResourceHintBar(m *Model) string {
	t := ActiveTheme
	sep := fg(t.BoxBorder, strings.Repeat("─", m.width))
	var hint string
	if m.isTTSPlaying() {
		var speedHint string
		if m.c2cfg.TTSBackend != "kokoro" {
			speedHint = fmt.Sprintf("  ·  %d wpm  [ slower  ] faster", m.ttsRate)
		}
		hint = renderWaveIndicator(m.spinnerFrame, "speaking"+speedHint+"  ·  s stop  ·  Ctrl+X close", t.StreamingText, t.Dimmed)
	} else {
		hint = fg(t.Dimmed, "↑↓ / PgUp PgDn  move  ·  s speak from here  ·  e edit  ·  g/G top/bottom  ·  Ctrl+X  close")
	}
	return sep + "\n" + strings.Repeat(" ", 1) + hint
}

func renderStatsLine(m *Model, sep string) string {
	t := ActiveTheme
	const pad = 1

	// Left: spinner + state indicator (transcribing → streaming → TTS playback).
	var left string
	if m.isTTSPlaying() {
		var ttsLabel string
		if m.c2cfg.TTSBackend == "kokoro" {
			ttsLabel = fmt.Sprintf("♪ #%d  kokoro", m.ttsExIdx+1)
		} else {
			ttsLabel = fmt.Sprintf("♪ #%d  %d wpm  [ slower  ] faster", m.ttsExIdx+1, m.ttsRate)
		}
		left = renderWaveIndicator(m.spinnerFrame, ttsLabel, t.StreamingText, t.Dimmed)
	} else if m.correcting {
		left = renderWaveIndicator(m.spinnerFrame, "correcting", t.StreamingText, t.Dimmed)
	} else if m.correctionFlash != "" {
		left = fg(t.StreamingText, m.correctionFlash)
	} else if m.streaming {
		left = renderWaveIndicator(m.spinnerFrame, "streaming", t.StreamingText, t.Dimmed)
	} else if m.transcribing {
		left = renderWaveIndicator(m.spinnerFrame, "transcribing", t.StreamingText, t.Dimmed)
	}

	// Center: per-request stats — shown permanently after first response.
	var center string
	if m.lastResult != nil {
		r := m.lastResult
		s := fmt.Sprintf("%d in · %d out · %dms", r.Usage.InputTokens, r.Usage.OutputTokens, r.Elapsed.Milliseconds())
		inPer1M, outPer1M, hasPricing := config.ExtractPricing(m.eng.Profile().Info)
		if hasPricing {
			cost := config.CalcCost(r.Usage.InputTokens, r.Usage.OutputTokens, inPer1M, outPer1M)
			s += " · " + config.FormatCost(cost)
		}
		center = fg(t.Dimmed, s)
	}

	// Right: topic + global cumulative stats.
	var right string
	if m.topicStats.Calls > 0 {
		right = fg(t.Dimmed, fmt.Sprintf("topic: %d · %s",
			m.topicStats.Calls, config.FormatCost(m.topicStats.CostUSD)))
		right += fg(t.Dimmed, fmt.Sprintf("  total: %d · %s",
			m.sessionStats.Calls, config.FormatCost(m.sessionStats.CostUSD)))
	}

	leftW := visibleWidth(left)
	centerW := visibleWidth(center)
	rightW := visibleWidth(right)

	// Position center in the middle of the bar.
	// left-pad so center lands at (width-centerW)/2.
	centerStart := (m.width - centerW) / 2
	leftEnd := pad + leftW
	gapLC := centerStart - leftEnd
	if gapLC < 1 {
		gapLC = 1
	}
	// right-pad fills remaining space.
	centerEnd := centerStart + centerW
	gapCR := m.width - pad - rightW - centerEnd
	if gapCR < 1 {
		gapCR = 1
	}

	statsLine := strings.Repeat(" ", pad) +
		left +
		strings.Repeat(" ", gapLC) +
		center +
		strings.Repeat(" ", gapCR) +
		right +
		strings.Repeat(" ", pad)
	return sep + "\n" + statsLine
}

// renderTopicPicker renders the topic picker overlay.
func renderTopicPicker(m *Model) string {
	t := ActiveTheme
	w := m.width

	title := fg(t.TopBarText, "Switch topic")
	cursor := ""
	if m.cursorVisible {
		cursor = fg(t.InputPrompt, "█")
	}
	filterDisplay := fg(t.InputText, m.topicPickerFilter) + cursor
	titleLine := " " + title + "  " + filterDisplay
	titleBar := titleLine + strings.Repeat(" ", max(0, w-lipgloss.Width(titleLine)))
	topSep := fg(t.BoxBorder, strings.Repeat("─", w))

	items := m.topicPickerItems
	total := len(items)
	scroll := m.topicPickerScroll
	end := scroll + topicPickerMaxVisible
	if end > total {
		end = total
	}
	current := m.eng.TopicName()

	var listLines []string
	for i, name := range items[scroll:end] {
		absIdx := scroll + i
		isCurrent := name == current
		isSelected := absIdx == m.topicPickerIdx
		marker := "  "
		if isSelected {
			marker = fg(t.InputPrompt, "> ")
		}
		var label string
		if isSelected {
			color := t.TopBarText
			if isCurrent {
				color = t.Spinner
			}
			label = lipgloss.NewStyle().Bold(true).Render(fg(color, name))
		} else if isCurrent {
			label = fg(t.Spinner, name)
		} else {
			label = fg(t.AssistantText, name)
		}
		listLines = append(listLines, " "+marker+label)
	}
	for len(listLines) < topicPickerMaxVisible {
		listLines = append(listLines, "")
	}

	midSep := fg(t.BoxBorder, strings.Repeat("─", w))

	var hintParts []string
	if scroll > 0 {
		hintParts = append(hintParts, fmt.Sprintf("↑%d more", scroll))
	}
	if end < total {
		hintParts = append(hintParts, fmt.Sprintf("↓%d more", total-end))
	}
	countStr := fmt.Sprintf("%d of %d topics", total, len(m.topicPickerAll))
	if len(hintParts) > 0 {
		countStr += "  (" + strings.Join(hintParts, "  ") + ")"
	}

	var sb strings.Builder
	sb.WriteString(titleBar + "\n")
	sb.WriteString(topSep + "\n")
	sb.WriteString(strings.Join(listLines, "\n") + "\n")
	sb.WriteString(midSep + "\n")
	sb.WriteString(" " + fg(t.Dimmed, countStr) + "\n")
	sb.WriteString(" " + fg(t.Dimmed, "↑↓ navigate  ·  type to filter  ·  Enter switch  ·  Esc / Ctrl+X close"))
	return sb.String()
}

// renderProfilePicker renders the profile picker overlay.
func renderProfilePicker(m *Model) string {
	t := ActiveTheme
	w := m.width

	title := fg(t.TopBarText, "Switch profile")
	cursor := ""
	if m.cursorVisible {
		cursor = fg(t.InputPrompt, "█")
	}
	filterDisplay := fg(t.InputText, m.profilePickerFilter) + cursor
	titleLine := " " + title + "  " + filterDisplay
	titleBar := titleLine + strings.Repeat(" ", max(0, w-lipgloss.Width(titleLine)))
	topSep := fg(t.BoxBorder, strings.Repeat("─", w))

	items := m.profilePickerItems
	total := len(items)
	scroll := m.profilePickerScroll
	end := scroll + topicPickerMaxVisible
	if end > total {
		end = total
	}
	current := m.eng.ProfileCode()

	var listLines []string
	for i, name := range items[scroll:end] {
		absIdx := scroll + i
		isCurrent := name == current
		isSelected := absIdx == m.profilePickerIdx
		marker := "  "
		if isSelected {
			marker = fg(t.InputPrompt, "> ")
		}
		var label string
		if isSelected {
			color := t.TopBarText
			if isCurrent {
				color = t.Spinner
			}
			label = lipgloss.NewStyle().Bold(true).Render(fg(color, name))
		} else if isCurrent {
			label = fg(t.Spinner, name)
		} else {
			label = fg(t.AssistantText, name)
		}
		listLines = append(listLines, " "+marker+label)
	}
	for len(listLines) < topicPickerMaxVisible {
		listLines = append(listLines, "")
	}

	midSep := fg(t.BoxBorder, strings.Repeat("─", w))

	var hintParts []string
	if scroll > 0 {
		hintParts = append(hintParts, fmt.Sprintf("↑%d more", scroll))
	}
	if end < total {
		hintParts = append(hintParts, fmt.Sprintf("↓%d more", total-end))
	}
	countStr := fmt.Sprintf("%d of %d profiles", total, len(m.profilePickerAll))
	if len(hintParts) > 0 {
		countStr += "  (" + strings.Join(hintParts, "  ") + ")"
	}

	var sb strings.Builder
	sb.WriteString(titleBar + "\n")
	sb.WriteString(topSep + "\n")
	sb.WriteString(strings.Join(listLines, "\n") + "\n")
	sb.WriteString(midSep + "\n")
	sb.WriteString(" " + fg(t.Dimmed, countStr) + "\n")
	sb.WriteString(" " + fg(t.Dimmed, "↑↓ navigate  ·  type to filter  ·  Enter switch  ·  Esc / Ctrl+X close"))
	return sb.String()
}
