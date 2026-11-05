package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ↓ moves the pick; a step whose Skip says so is never visited.
func TestFormSelectAndSkip(t *testing.T) {
	layout, custom := "A", ""
	fields := []*Field{
		{Kind: FieldSelect, Title: "Layout", Options: []string{"A", "B", "Custom"}, Value: &layout},
		{Kind: FieldInput, Title: "Rules", Value: &custom, Skip: func() bool { return layout != "Custom" }},
		{Kind: FieldInput, Title: "Home", Value: new(string)},
	}
	m := NewFormModel(fields, nil)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if layout != "B" {
		t.Fatalf("down picked %q, want B", layout)
	}
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := next.(FormModel).Current; got != 2 {
		t.Errorf("enter after a preset landed on step %d, want 2 (the rules step skipped)", got)
	}
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyDown})
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := next.(FormModel).Current; got != 1 {
		t.Errorf("enter after Custom landed on step %d, want the rules step", got)
	}
}

// Enter edits a row; a save re-reads the list and tells the shell; esc saves without asking.
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
	m, _ = run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	m, msgs = run(m, tea.KeyMsg{Type: tea.KeyEsc})
	if saves != 2 || value != "older!" || len(msgs) != 1 || m.(SettingsModel).edit != nil {
		t.Errorf("esc should save and close the edit: saves=%d value=%q msgs=%v", saves, value, msgs)
	}
}

// Enter pressed repeatedly on a one-step row saves once and never crashes,
// whether the extra presses land before or after the list takes the form away.
func TestSettingsListEnterRepeatedly(t *testing.T) {
	saves := 0
	rows := func() []SettingRow {
		return []SettingRow{{Label: "Library", Value: "/lib", Edit: func() ([]*Field, func() error) {
			v := "/lib"
			return []*Field{{Kind: FieldInput, Title: "Which library?", Value: &v}}, func() error { saves++; return nil }
		}}}
	}
	var m tea.Model = NewSettingsModel(rows, "")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	for range 4 { // open the row, then press enter again and again
		m, _ = m.Update(enter)
	}
	if saves != 1 {
		t.Errorf("saved %d times, want once", saves)
	}
}

// Only the value Enter changes is boxed, so every value starts in the same
// column whether or not its row is picked, and no arrow trails the row.
func TestSettingsRowsKeepTheValueColumn(t *testing.T) {
	rows := []SettingRow{
		{Label: "Library folder", Value: "/lib", Edit: func() ([]*Field, func() error) { return nil, nil }},
		{Label: "Home", Value: "Pune", Edit: func() ([]*Field, func() error) { return nil, nil }},
	}
	m := NewSettingsModel(func() []SettingRow { return rows }, "")
	m.w, m.h = 80, 24

	// col is the cell a value starts at within its line
	col := func(view, value string) int {
		text := viewText(view)
		at := strings.Index(text, value)
		return at - strings.LastIndex(text[:at], "\n")
	}
	atFirst := m.View()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	atSecond := next.(SettingsModel).View()
	for _, value := range []string{"/lib", "Pune"} {
		if a, b := col(atFirst, value), col(atSecond, value); a != b {
			t.Errorf("%s moved from column %d to %d when the pick moved", value, a, b)
		}
	}
	if strings.Contains(viewText(atFirst), "›") {
		t.Errorf("no arrow belongs at the end of a row:\n%s", viewText(atFirst))
	}
}
