package tui

import tea "github.com/charmbracelet/bubbletea"

// Tab is a screen the app shell hosts. Busy is the one fact about a screen
// the container can't work out itself.
type Tab interface {
	tea.Model
	// Busy reports work in flight that must not be interrupted or replaced (a
	// running scan).
	Busy() bool
}

// Leave is the only way a hosted screen hands control back. Screens never
// return tea.Quit: that would end every other tab too.
type Leave struct {
	// Quit asks to end the session rather than go home; the container may
	// still let a running scan ask first.
	Quit bool
	// Aborted marks a screen left without its work being kept, so the
	// container knows not to act on an answer that was never given.
	Aborted bool
	// Err is why the screen ended, when it ended badly.
	Err error
	// Note is one line for the home screen: what was saved, what was kept.
	Note string
}

// Left is the command form of Leave, for a screen to return from Update.
func Left(l Leave) tea.Cmd { return func() tea.Msg { return l } }
