package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A digit picks a numbered choice; a step whose Skip says so is never visited.
func TestFormSelectAndSkip(t *testing.T) {
	layout, custom := "A", ""
	fields := []*Field{
		{Kind: FieldSelect, Title: "Layout", Options: []string{"A", "B", "Custom"}, Value: &layout},
		{Kind: FieldInput, Title: "Rules", Value: &custom, Skip: func() bool { return layout != "Custom" }},
		{Kind: FieldInput, Title: "Home", Value: new(string)},
	}
	m := NewFormModel(fields, nil)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	if layout != "B" {
		t.Fatalf("2 picked %q, want B", layout)
	}
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := next.(FormModel).Current; got != 2 {
		t.Errorf("enter after a preset landed on step %d, want 2 (the rules step skipped)", got)
	}
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := next.(FormModel).Current; got != 1 {
		t.Errorf("enter after Custom landed on step %d, want the rules step", got)
	}
}

// Enter edits a row; saving re-reads the list and tells the shell; leaving the
// edit without saving does neither.
func TestSettingsListEdit(t *testing.T) {
	value, saves := "old", 0
	rows := func() []SettingRow {
		return []SettingRow{
			{Label: "Library", Value: "/lib"},
			{Label: "Home", Value: value, Edit: func() ([]*Field, func() error) {
				typed := value
				return []*Field{{Kind: FieldInput, Title: "Home", Value: &typed}},
					func() error { saves++; value = typed; return nil }
			}},
		}
	}
	run := func(m tea.Model, msg tea.Msg) (tea.Model, []tea.Msg) {
		next, cmd := m.Update(msg)
		var out []tea.Msg
		for _, msg := range flattenCmd(cmd) {
			if done, ok := msg.(settingsEditDoneMsg); ok {
				next, cmd = next.Update(done)
				out = append(out, flattenCmd(cmd)...)
				continue
			}
			out = append(out, msg)
		}
		return next, out
	}

	var m tea.Model = NewSettingsModel(rows, "")
	if m.(SettingsModel).cursor != 1 {
		t.Fatalf("cursor starts on row %d, want the first changeable one", m.(SettingsModel).cursor)
	}
	m, _ = run(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("er")})
	m, msgs := run(m, tea.KeyMsg{Type: tea.KeyEnter})
	if saves != 1 || value != "older" || len(msgs) != 1 || msgs[0] != (SettingsSavedMsg{}) {
		t.Fatalf("save: saves=%d value=%q msgs=%v", saves, value, msgs)
	}
	if got := m.(SettingsModel).list[1].Value; got != "older" {
		t.Errorf("list shows %q after the save, want the new value", got)
	}

	m, _ = run(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = run(m, tea.KeyMsg{Type: tea.KeyEsc})
	m, msgs = run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}) // discard
	if saves != 1 || len(msgs) != 0 || m.(SettingsModel).edit != nil {
		t.Errorf("a discarded edit saved or stayed open: saves=%d msgs=%v", saves, msgs)
	}
}
