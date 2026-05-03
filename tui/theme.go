package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme holds foreground colors only. No background colors are set —
// the terminal's own background is used throughout.
type Theme struct {
	// Text
	TopBarText    lipgloss.Color
	UserText      lipgloss.Color
	AssistantText lipgloss.Color
	InputPrompt   lipgloss.Color
	Dimmed        lipgloss.Color

	// Accents
	Spinner        lipgloss.Color
	ContextNormal  lipgloss.Color
	ContextWarning lipgloss.Color // bold yellow when fill >= 90%

	// Streaming indicator (left of status bar)
	StreamingText lipgloss.Color

	// Input pane
	InputText lipgloss.Color

	// Status bar
	StatusText lipgloss.Color

	// Box border (focused exchange)
	BoxBorder lipgloss.Color
}

// Nord is a cool-blues dark theme based on the Nord palette.
// https://www.nordtheme.com/
var Nord = Theme{
	TopBarText:    "#C79BFF", // purple
	UserText:      "#81A1C1", // nord9  — steel blue
	AssistantText: "#D8DEE9", // nord4  — soft white
	InputPrompt:   "#D8DEE9", // nord4  — same as input text
	Dimmed:        "#4C566A", // nord3  — dark gray

	Spinner:        "#88C0D0", // nord8  — light cyan
	ContextNormal:  "#D8DEE9", // nord4
	ContextWarning: "#EBCB8B", // nord13 — yellow

	StreamingText: "#C79BFF", // purple
	InputText:     "#D8DEE9", // nord4  — soft white
	StatusText: "#D8DEE9", // nord4
	BoxBorder:  "#88C0D0", // nord8
}

// ClaudeCode approximates the color palette used by Claude Code's TUI.
// Colors extracted from the Claude Code binary (chalk/ink-based rendering).
//
// Key semantic colors:
//
//	body/claude: rgb(215,119,87)  — Claude's signature orange
//	you/user:    rgb(122,180,232) — steel blue for user turns
//	spinner:     rgb(101,152,255) — blue-purple spinner frames
//	warning:     rgb(251,188,4)   — chromeYellow
var ClaudeCode = Theme{
	TopBarText:    "#D7D7D7", // soft white
	UserText:      "#7AB4E8", // rgb(122,180,232) — briefLabelYou
	AssistantText: "#D77757", // rgb(215,119,87)  — body/claude orange
	InputPrompt:   "#D7D7D7", // same as input text
	Dimmed:        "#505050", // rgb(80,80,80)    — subtle

	Spinner:        "#6598FF", // rgb(101,152,255) — SPINNER base frame
	ContextNormal:  "#D7D7D7",
	ContextWarning: "#FABE04", // rgb(251,188,4)   — chromeYellow

	StreamingText: "#C79BFF", // purple
	InputText:     "#D7D7D7",
	StatusText: "#D7D7D7",
	BoxBorder:  "#6598FF",
}

// Light is a theme for iTerm2 profiles with a light background.
// All colors are chosen for readability against a white/light background.
var Light = Theme{
	TopBarText:    "#5A2D9A", // deep purple
	UserText:      "#1A5E8A", // dark blue
	AssistantText: "#2E2E2E", // near-black
	InputPrompt:   "#2E2E2E", // near-black
	Dimmed:        "#9A9A9A", // medium gray

	Spinner:        "#1A7AB0", // dark cyan
	ContextNormal:  "#2E2E2E",
	ContextWarning: "#B8600A", // dark orange

	StreamingText: "#5A2D9A", // deep purple
	InputText:     "#2E2E2E", // near-black
	StatusText:    "#2E2E2E",
	BoxBorder:     "#1A7AB0", // dark cyan
}

// ActiveTheme is the theme used by all view functions.
// Set at startup based on terminal background detection.
var ActiveTheme = Nord

// ApplyTheme sets ActiveTheme from a mode string: "light", "dark", or "auto".
// "auto" detects from COLORFGBG; unknown values fall back to auto.
func ApplyTheme(mode string) {
	switch strings.ToLower(mode) {
	case "light":
		ActiveTheme = Light
	case "dark":
		ActiveTheme = Nord
	default: // "auto" or unset
		DetectTheme()
	}
}

// DetectTheme sets ActiveTheme based on terminal-specific heuristics.
// On iTerm2, COLORFGBG is read (fg;bg — bg >= 8 means light).
// On other terminals, defaults to dark.
func DetectTheme() {
	switch ActiveTerminal {
	case TermITerm2:
		// iTerm2 sets COLORFGBG="fg;bg"; bg >= 8 means light background.
		fgbg := os.Getenv("COLORFGBG")
		if fgbg == "" {
			return
		}
		parts := strings.SplitN(fgbg, ";", 2)
		if len(parts) != 2 {
			return
		}
		var bg int
		fmt.Sscanf(parts[1], "%d", &bg)
		if bg >= 8 {
			ActiveTheme = Light
		}
	case TermApple:
		// Terminal.app has no env var for background color — use OSC 11 query.
		if queryBackgroundLight() {
			ActiveTheme = Light
		}
	default:
		// No reliable detection — stay with default dark theme.
	}
}
