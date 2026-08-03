// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

var errBadValue = errors.New("bad value")

func TestConfirmModelYesNoKeys(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyMsg
		want bool
	}{
		{"y", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}, true},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, true},
		{"n", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}, false},
		{"escape", tea.KeyMsg{Type: tea.KeyEsc}, false},
		{"left", tea.KeyMsg{Type: tea.KeyLeft}, true},
		{"right", tea.KeyMsg{Type: tea.KeyRight}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := !tt.want // start on the opposite value so we can tell the key acted
			m := NewConfirmModel("Proceed?", "", &v)
			next, _ := m.Update(tt.key)
			m = next.(ConfirmModel)
			if v != tt.want {
				t.Errorf("key %q: value = %v, want %v", tt.name, v, tt.want)
			}
		})
	}
}

func TestConfirmModelQuitsOnTerminalKeys(t *testing.T) {
	v := false
	m := NewConfirmModel("Proceed?", "", &v)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("enter should quit the confirm model")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("enter's cmd should be tea.Quit")
	}

	// left/right (rebind, don't commit) must NOT quit.
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if cmd != nil {
		t.Errorf("left should only flip the highlighted side, not quit")
	}
}

func TestConfirmModelCtrlCAborts(t *testing.T) {
	v := true
	m := NewConfirmModel("Proceed?", "", &v)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(ConfirmModel)
	if !m.IsAborted() {
		t.Errorf("ctrl+c should mark the model aborted")
	}
	if cmd == nil {
		t.Fatalf("ctrl+c should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c's cmd should be tea.Quit")
	}
}

func TestConfirmModelWindowSizeAndView(t *testing.T) {
	v := true
	m := NewConfirmModel("Proceed?", "a description", &v)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = next.(ConfirmModel)

	view := ansi.Strip(m.View())
	for _, want := range []string{"Proceed?", "a description", "yes", "no"} {
		if !strings.Contains(view, want) {
			t.Errorf("ConfirmModel.View() missing %q:\n%s", want, view)
		}
	}
}

func TestConfirmModelInitReturnsNilCmd(t *testing.T) {
	m := NewConfirmModel("t", "", nil)
	if m.Init() != nil {
		t.Errorf("ConfirmModel.Init() should return nil, no async work to kick off")
	}
}

// TestFormSaveAndExitCommitsActiveInputAndSubmits pins the escape hatch: a
// user who only wants to change the field they're on can press the save key
// without stepping through every remaining field.
func TestFormSaveAndExitCommitsActiveInputAndSubmits(t *testing.T) {
	out := ""
	submitted := false
	fields := []*Field{
		{Kind: FieldInput, Title: "Output path", Value: &out},
		{Kind: FieldInput, Title: "Unused", Value: new(string)},
	}
	m := NewFormModel(fields, func() error { submitted = true; return nil })
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/tmp/x")})

	next, cmd := m.saveAndExit()
	m = next.(FormModel)
	if out != "/tmp/x" {
		t.Errorf("saveAndExit must commit the active input, got %q", out)
	}
	if !submitted {
		t.Errorf("saveAndExit must call onSubmit even with fields unanswered")
	}
	if cmd == nil {
		t.Fatalf("saveAndExit should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("saveAndExit's cmd should be tea.Quit")
	}
}

func TestFormSaveAndExitBlockedByValidator(t *testing.T) {
	out := ""
	fields := []*Field{
		{Kind: FieldInput, Title: "Output path", Value: &out, Validator: func(string) error {
			return errBadValue
		}},
	}
	m := NewFormModel(fields, nil)
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})

	next, cmd := m.saveAndExit()
	m = next.(FormModel)
	if cmd != nil {
		t.Errorf("a failing validator must block saveAndExit, got cmd %v", cmd)
	}
	if m.active().Error == "" {
		t.Errorf("saveAndExit must surface the validator error on the field")
	}
}

func TestFormSaveAndExitBlockedWhileAwaiting(t *testing.T) {
	fields := []*Field{
		{Kind: FieldInput, Title: "Town", Value: new(string), Await: func() string { return "still waiting" }},
	}
	m := NewFormModel(fields, nil)

	_, cmd := m.saveAndExit()
	if cmd != nil {
		t.Errorf("saveAndExit must not quit while the active field is held, got cmd %v", cmd)
	}
}

func TestFormMovePrevWalksBackThroughGroupSubs(t *testing.T) {
	fields := []*Field{
		{Kind: FieldInput, Title: "First", Value: new(string)},
		{Kind: FieldGroup, Title: "Towns", Subs: []*Field{
			{Kind: FieldInput, Title: "Home", Value: new(string)},
			{Kind: FieldInput, Title: "Work", Value: new(string)},
		}},
	}
	m := NewFormModel(fields, nil)
	m.Current = 1
	m.subIdx = 1 // on the group's second sub-field

	next, _ := m.movePrev()
	m = next.(FormModel)
	if m.Current != 1 || m.subIdx != 0 {
		t.Fatalf("movePrev inside a group should step back a sub first, got Current=%d subIdx=%d", m.Current, m.subIdx)
	}

	next, _ = m.movePrev()
	m = next.(FormModel)
	if m.Current != 0 {
		t.Errorf("movePrev at the group's first sub should back into the previous field, got Current=%d", m.Current)
	}
}

func TestFormMovePrevBackIntoGroupLandsOnLastSub(t *testing.T) {
	fields := []*Field{
		{Kind: FieldGroup, Title: "Towns", Subs: []*Field{
			{Kind: FieldInput, Title: "Home", Value: new(string)},
			{Kind: FieldInput, Title: "Work", Value: new(string)},
		}},
		{Kind: FieldInput, Title: "After", Value: new(string)},
	}
	m := NewFormModel(fields, nil)
	m.Current = 1

	next, _ := m.movePrev()
	m = next.(FormModel)
	if m.Current != 0 || m.subIdx != 1 {
		t.Errorf("stepping back into a group should land on its last sub, got Current=%d subIdx=%d", m.Current, m.subIdx)
	}
}

func TestFormMovePrevAtFirstFieldIsNoop(t *testing.T) {
	fields := []*Field{{Kind: FieldInput, Title: "Only", Value: new(string)}}
	m := NewFormModel(fields, nil)

	next, _ := m.movePrev()
	m = next.(FormModel)
	if m.Current != 0 {
		t.Errorf("movePrev at field 0 must stay put, got Current=%d", m.Current)
	}
}
