package tui

import tea "github.com/charmbracelet/bubbletea"

// SwitchMsg hands the shell a screen for another tab, kept or opened at once (Open); done screens use Leave.
type SwitchMsg struct {
	Next Tab
	Open bool
}

// Switch is the tea.Cmd a screen returns to hand over the screen it built.
func Switch(next Tab) tea.Cmd {
	return func() tea.Msg { return SwitchMsg{Next: next} }
}
