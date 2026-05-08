package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap defines all c2 key bindings.
type keyMap struct {
	Send          key.Binding
	Newline       key.Binding
	Cancel        key.Binding
	SwitchTopic   key.Binding
	SwitchProfile key.Binding
	ClearScreen   key.Binding
	NavUp         key.Binding
	NavDown       key.Binding
	ScrollUp      key.Binding
	ScrollDown    key.Binding
	Dismiss       key.Binding
	FocusConv     key.Binding
	FillCompletion key.Binding
	SwitchMode        key.Binding
	CopyToClipboard       key.Binding
	WakeWord key.Binding // Ctrl+Space: toggle AWAKE state (voice mode only)
	DEVToggleTranscribing key.Binding // dev-only: simulate transcribing state
}

var keys = keyMap{
	Send: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "send"),
	),
	Newline: key.NewBinding(
		key.WithKeys("ctrl+j"),
		key.WithHelp("ctrl+j", "newline"),
	),
	Cancel: key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("ctrl+c", "cancel/quit"),
	),
	SwitchTopic: key.NewBinding(
		key.WithKeys("ctrl+t"),
		key.WithHelp("ctrl+t", "switch topic"),
	),
	SwitchProfile: key.NewBinding(
		key.WithKeys("ctrl+p"),
		key.WithHelp("ctrl+p", "switch profile"),
	),
	ClearScreen: key.NewBinding(
		key.WithKeys("ctrl+l"),
		key.WithHelp("ctrl+l", "clear screen"),
	),
	NavUp: key.NewBinding(
		key.WithKeys("up"),
		key.WithHelp("↑", "prev exchange"),
	),
	NavDown: key.NewBinding(
		key.WithKeys("down"),
		key.WithHelp("↓", "next exchange"),
	),
	ScrollUp: key.NewBinding(
		key.WithKeys("pgup"),
		key.WithHelp("pgup", "scroll up"),
	),
	ScrollDown: key.NewBinding(
		key.WithKeys("pgdown"),
		key.WithHelp("pgdn", "scroll down"),
	),
	Dismiss: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back to input"),
	),
	FocusConv: key.NewBinding(
		key.WithKeys("ctrl+n"),
		key.WithHelp("ctrl+n", "toggle focus: input ↔ conversation"),
	),
	FillCompletion: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "fill selected completion into input"),
	),
	SwitchMode: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch voice/text mode"),
	),
	CopyToClipboard: key.NewBinding(
		key.WithKeys("ctrl+s"),
		key.WithHelp("ctrl+s", "copy to clipboard"),
	),
	WakeWord: key.NewBinding(
		key.WithKeys("ctrl+@"),
		key.WithHelp("ctrl+space", "activate/cancel voice command (voice mode only)"),
	),
	DEVToggleTranscribing: key.NewBinding(
		key.WithKeys("ctrl+y"),
		key.WithHelp("ctrl+y", "[DEV] toggle transcribing state"),
	),
}
