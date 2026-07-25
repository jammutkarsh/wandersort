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

// Options are picked by number, examples render outside the description (only
// for the choice under the cursor), and a field can hold on a background
// download instead of letting the user answer it.
func TestFormNumberedExampleAwait(t *testing.T) {
	dateOnly := true
	town := ""
	pending := true
	fields := []*Field{{
		Kind:  FieldGroup,
		Title: "Home & work",
		Await: func() string {
			if pending {
				return "Waiting for the location database"
			}
			return ""
		},
		Subs: []*Field{
			{Kind: FieldInput, Title: "Home town", Value: &town},
			{
				Kind: FieldConfirm, Title: "Date only?", Description: "explanation only",
				BoolValue: &dateOnly,
				Example: func() string {
					if dateOnly {
						return "2024/08/12/IMG.jpg"
					}
					return "2024/08/12/Indore/IMG.jpg"
				},
			},
		},
	}}
	m := NewFormModel(fields, nil)

	// Held: the reason shows and enter can't get past it.
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Waiting for the location database") {
		t.Errorf("held field must say what it waits for:\n%s", view)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m2 := next.(FormModel); m2.subIdx != 0 {
		t.Errorf("enter must not advance a held field, subIdx=%d", m2.subIdx)
	}

	// Released by the download finishing.
	pending = false
	next, _ = m.Update(DownloadMsg{Finished: true})
	m = next.(FormModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(FormModel)
	if m.subIdx != 1 {
		t.Fatalf("enter should reach the confirm sub, subIdx=%d", m.subIdx)
	}

	// "2" picks no — by number, without advancing — and only that option's
	// example renders, outside the description.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = next.(FormModel)
	if dateOnly {
		t.Error(`"2" should select no`)
	}
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "1) ") || !strings.Contains(view, "2) ") {
		t.Errorf("options must be numbered:\n%s", view)
	}
	if !strings.Contains(view, "2024/08/12/Indore/IMG.jpg") || strings.Contains(view, "2024/08/12/IMG.jpg\n") {
		t.Errorf("only the selected option's example should render:\n%s", view)
	}
	if strings.Contains(view, "explanation only\n    2024/") {
		t.Errorf("example must not sit inside the description:\n%s", view)
	}
}

// The download row shows progress under the banner and disappears when done.
func TestFormDownloadRow(t *testing.T) {
	v := ""
	m := NewFormModel([]*Field{{Kind: FieldInput, Title: "Output path", Value: &v}}, nil)
	m.w, m.h = 80, 24

	if strings.Contains(ansi.Strip(m.View()), "Location database") {
		t.Error("nothing should render before the first download report")
	}
	next, _ := m.Update(DownloadMsg{Label: "Location database", Done: 5, Total: 10})
	m = next.(FormModel)
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Location database") || !strings.Contains(view, "50%") {
		t.Errorf("download row missing progress:\n%s", view)
	}
	next, _ = m.Update(DownloadMsg{Finished: true})
	if view := ansi.Strip(next.(FormModel).View()); strings.Contains(view, "Location database") {
		t.Errorf("finished download must leave no trace:\n%s", view)
	}
}
