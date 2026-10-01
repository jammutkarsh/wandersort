package tui

import tea "github.com/charmbracelet/bubbletea"

// SwitchMsg hands the container a screen for another tab (the scan's
// prefetched review), to keep, or to open at once when Open is set. Screens
// that are done use Leave.
type SwitchMsg struct {
	Next Tab
	Open bool
}

// Switch is the tea.Cmd a screen returns to hand over the screen it built.
func Switch(next Tab) tea.Cmd {
	return func() tea.Msg { return SwitchMsg{Next: next} }
}
