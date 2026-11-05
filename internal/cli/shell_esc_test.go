package cli

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// esc inside a sub-menu only closes it; "esc again to quit" belongs to the main screen.
func TestEscInASubMenuDoesNotArmQuit(t *testing.T) {
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	m := testShell(t)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")}) // the key list is a sub-menu
	m = next.(shellModel)
	next, _ = m.Update(esc)
	m = next.(shellModel)
	if m.escArmed || m.notice != "" {
		t.Fatalf("esc out of the key list armed the quit: armed=%v notice=%q", m.escArmed, m.notice)
	}

	next, _ = m.Update(esc) // now on the main screen
	m = next.(shellModel)
	if !m.escArmed || m.notice == "" {
		t.Errorf("esc on the main screen should offer the quit: armed=%v notice=%q", m.escArmed, m.notice)
	}
}
