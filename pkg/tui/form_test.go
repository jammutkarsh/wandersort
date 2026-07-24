// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The form renders as one top-down stack: answered fields collapse to a
// summary row with their value, the current field expands, pending fields
// stay visible but dim.
func TestFormStackedView(t *testing.T) {
	out := "/tmp/lib"
	yes := true
	fields := []*Field{
		{Kind: FieldInput, Title: "Output path", Value: &out},
		{Kind: FieldConfirm, Title: "Collapse levels?", BoolValue: &yes},
		{Kind: FieldMultiSelect, Title: "Rules", Options: []string{"date", "location"}, Selected: map[string]bool{"date": true}},
	}
	m := NewFormModel(fields, nil)
	m.Current = 2 // first two answered

	view := ansi.Strip(m.View())
	for _, want := range []string{"/tmp/lib", "yes", "date", "location", "Output path", "Collapse levels?", "Rules"} {
		if !strings.Contains(view, want) {
			t.Errorf("stacked view missing %q:\n%s", want, view)
		}
	}
}

func TestFormSummaryValues(t *testing.T) {
	m := FormModel{}
	blank := "  "
	if got := m.summaryValue(&Field{Kind: FieldInput, Value: &blank}); got != "—" {
		t.Errorf("blank input summary = %q, want —", got)
	}
	no := false
	if got := m.summaryValue(&Field{Kind: FieldConfirm, BoolValue: &no}); got != "no" {
		t.Errorf("confirm summary = %q, want no", got)
	}
	f := &Field{Kind: FieldMultiSelect, Options: []string{"a", "b", "c"}, Selected: map[string]bool{"c": true, "a": true}}
	if got := m.summaryValue(f); got != "a, c" {
		t.Errorf("multiselect summary = %q, want \"a, c\" (option order)", got)
	}
}

// A FieldGroup answers its sub-inputs in order on one screen; the collapsed
// summary joins the answers.
func TestFormGroupField(t *testing.T) {
	home, work := "", ""
	done := false
	fields := []*Field{{Kind: FieldGroup, Title: "Towns", Subs: []*Field{
		{Kind: FieldInput, Title: "Home", Value: &home},
		{Kind: FieldInput, Title: "Work", Value: &work},
	}}}
	m := NewFormModel(fields, func() error { done = true; return nil })

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Delhi")})
	m = next.(FormModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(FormModel)
	if home != "Delhi" || m.subIdx != 1 {
		t.Fatalf("enter should commit sub 0 and focus sub 1: home=%q subIdx=%d", home, m.subIdx)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Goa")})
	m = next.(FormModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(FormModel)
	if work != "Goa" || !done {
		t.Fatalf("enter on last sub should finish the form: work=%q done=%v", work, done)
	}
	if got := m.summaryValue(fields[0]); got != "Delhi / Goa" {
		t.Errorf("group summary = %q, want \"Delhi / Goa\"", got)
	}
}

// ↓ highlights a suggestion, enter picks it instead of advancing the form.
func TestFormSuggestArrowPick(t *testing.T) {
	v := ""
	fields := []*Field{{Kind: FieldInput, Title: "Town", Value: &v, Suggest: func(typed string) []string {
		if typed == "" {
			return nil
		}
		return []string{"Delhi", "Dehradun"}
	}}}
	m := NewFormModel(fields, nil)

	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'d'}},
		{Type: tea.KeyDown},
		{Type: tea.KeyDown},
		{Type: tea.KeyEnter},
	} {
		next, _ := m.Update(k)
		m = next.(FormModel)
	}
	if v != "Dehradun" {
		t.Errorf("↓↓ + enter should pick the second suggestion, got %q", v)
	}
	if m.Current != 0 {
		t.Errorf("picking a suggestion must not advance the form")
	}
}

func TestFormSuggestAndTabFill(t *testing.T) {
	v := ""
	fields := []*Field{{Kind: FieldInput, Title: "Town", Value: &v, Suggest: func(typed string) []string {
		if typed == "" {
			return nil
		}
		return []string{"Delhi", "Dehradun"}
	}}}
	m := NewFormModel(fields, nil)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = next.(FormModel)
	if len(m.sugg) != 2 {
		t.Fatalf("typing should refresh suggestions, got %v", m.sugg)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(FormModel)
	if v != "Delhi" {
		t.Errorf("tab should fill top suggestion, got %q", v)
	}
}
