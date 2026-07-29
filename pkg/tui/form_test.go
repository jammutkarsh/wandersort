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

// flattenCmd executes cmd (and recursively any tea.Batch it produces),
// returning every resulting message in order.
func flattenCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, flattenCmd(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// sendKey feeds a keystroke through Update and, since Suggest now runs behind
// a debounce (see suggestDebounce), also drives the resulting
// suggestDebounceMsg/suggestResultMsg round-trip to completion so tests see
// the same end state the old synchronous Suggest call used to produce
// immediately.
func sendKey(m FormModel, k tea.KeyMsg) FormModel {
	next, cmd := m.Update(k)
	m = next.(FormModel)
	for _, msg := range flattenCmd(cmd) {
		if _, ok := msg.(suggestDebounceMsg); !ok {
			continue
		}
		next, cmd := m.Update(msg)
		m = next.(FormModel)
		for _, msg := range flattenCmd(cmd) {
			if _, ok := msg.(suggestResultMsg); !ok {
				continue
			}
			next, _ := m.Update(msg)
			m = next.(FormModel)
		}
	}
	return m
}

func TestForm(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// The form renders as one top-down stack: answered fields collapse to a
		// summary row with their value, the current field expands, pending fields
		// stay visible but dim.
		{"FormStackedView", func(t *testing.T) {
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
		}},
		// A FieldGroup answers its sub-inputs in order on one screen; the collapsed
		// summary joins the answers.
		{"FormGroupField", func(t *testing.T) {
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
		}},
		// ↓ highlights a suggestion, enter picks it instead of advancing the form.
		{"FormSuggestArrowPick", func(t *testing.T) {
			v := ""
			fields := []*Field{{Kind: FieldInput, Title: "Town", Value: &v, Suggest: func(typed string) []string {
				if typed == "" {
					return nil
				}
				return []string{"Delhi", "Dehradun"}
			}}}
			m := NewFormModel(fields, nil)

			m = sendKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
			for _, k := range []tea.KeyMsg{
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
		}},
		{"FormSuggestAndTabFill", func(t *testing.T) {
			v := ""
			fields := []*Field{{Kind: FieldInput, Title: "Town", Value: &v, Suggest: func(typed string) []string {
				if typed == "" {
					return nil
				}
				return []string{"Delhi", "Dehradun"}
			}}}
			m := NewFormModel(fields, nil)

			m = sendKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
			if len(m.sugg) != 2 {
				t.Fatalf("typing should refresh suggestions, got %v", m.sugg)
			}
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
			m = next.(FormModel)
			if v != "Delhi" {
				t.Errorf("tab should fill top suggestion, got %q", v)
			}
		}},
		// Examples render outside the description (only for the choice under the
		// cursor), and a field can hold on a background download instead of letting
		// the user answer it.
		{"FormExampleAwait", func(t *testing.T) {
			dateOnly := true
			town := ""
			pending := true
			fields := []*Field{{
				Kind:  FieldGroup,
				Title: "Saved places",
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

			// ↓ picks no, without advancing — and only that option's example renders,
			// outside the description.
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
			m = next.(FormModel)
			if dateOnly {
				t.Error("down should select no")
			}
			view = ansi.Strip(m.View())
			if !strings.Contains(view, "2024/08/12/Indore/IMG.jpg") || strings.Contains(view, "2024/08/12/IMG.jpg\n") {
				t.Errorf("only the selected option's example should render:\n%s", view)
			}
			if strings.Contains(view, "explanation only\n    2024/") {
				t.Errorf("example must not sit inside the description:\n%s", view)
			}
		}},
		// The step list — not the options inside a step — is numbered, and a digit
		// key jumps straight to that step. A digit typed into a text field is
		// ordinary input, never a jump.
		{"FormStepNumberingAndJump", func(t *testing.T) {
			out := ""
			rules := map[string]bool{"date": true}
			fields := []*Field{
				{Kind: FieldInput, Title: "Output path", Value: &out},
				{Kind: FieldMultiSelect, Title: "Rules", Options: []string{"date", "location"}, Selected: rules},
			}
			m := NewFormModel(fields, nil)
			m.w, m.h = 80, 24

			view := ansi.Strip(m.View())
			if !strings.Contains(view, "1) Output path") || !strings.Contains(view, "2) Rules") {
				t.Errorf("steps must be numbered, options must not:\n%s", view)
			}
			if strings.Contains(view, "1) ○") || strings.Contains(view, "2) ○") {
				t.Errorf("option markers must not carry step-style numbers:\n%s", view)
			}

			// A digit typed into the focused text field is ordinary input, not a jump.
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
			m = next.(FormModel)
			if m.Current != 0 || out != "2" {
				t.Errorf("digit in a text field must type, not jump: Current=%d out=%q", m.Current, out)
			}

			// "2" now jumps to step 2 once the active field isn't a text input.
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(FormModel)
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
			m = next.(FormModel)
			if m.Current != 0 {
				t.Errorf("jumping back to step 1 from step 2 failed: Current=%d", m.Current)
			}
		}},
		// The download row shows progress under the banner and disappears when done.
		{"FormDownloadRow", func(t *testing.T) {
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
			// Finished carries no label of its own; the done line must keep the
			// one from the byte reports rather than vanishing mid-glance.
			next, _ = m.Update(DownloadMsg{Finished: true})
			if view := ansi.Strip(next.(FormModel).View()); !strings.Contains(view, "Location database · done") {
				t.Errorf("finished download should persist as a done line:\n%s", view)
			}
		}},
		// FormExampleSidePanel: on a wide terminal the example renders as a
		// right-hand column (bordered box); on a narrow one it falls back to the
		// block above the footer. Same content either way.
		{"FormExampleSidePanel", func(t *testing.T) {
			yes := true
			m := NewFormModel([]*Field{{
				Kind: FieldConfirm, Title: "Merge?", BoolValue: &yes,
				Example: func() string { return "2024\n└─ 08_August" },
			}}, nil)

			m.w, m.h = 140, 30
			if !m.sidePanel() {
				t.Fatal("wide terminal with an example should use the side panel")
			}
			wide := ansi.Strip(m.View())
			if !strings.Contains(wide, "└─ 08_August") || !strings.Contains(wide, "╭") {
				t.Errorf("side panel missing example box:\n%s", wide)
			}

			m.w = 80
			if m.sidePanel() {
				t.Fatal("narrow terminal must fall back to the footer block")
			}
			if narrow := ansi.Strip(m.View()); !strings.Contains(narrow, "└─ 08_August") {
				t.Errorf("footer example block missing:\n%s", narrow)
			}
		}},
		// FormDownloadAlreadyInstalled: a dependency already on disk sends only
		// the Finished message — the form must never mention it at all.
		{"FormDownloadAlreadyInstalled", func(t *testing.T) {
			v := ""
			m := NewFormModel([]*Field{{Kind: FieldInput, Title: "Output path", Value: &v}}, nil)
			m.w, m.h = 80, 24
			next, _ := m.Update(DownloadMsg{Finished: true})
			if view := ansi.Strip(next.(FormModel).View()); strings.Contains(view, "done") {
				t.Errorf("an install that never reported bytes must stay invisible:\n%s", view)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

// A description wraps to the body width instead of losing its tail to
// truncation, and Describe wins over Description when both are set.
func TestFormDescriptionBlock(t *testing.T) {
	m := FormModel{w: 100} // side panel showing: body is well under 100 cols
	long := "one two three four five six seven eight nine ten eleven twelve thirteen fourteen"
	f := &Field{Description: "static", Describe: func() string { return long }}

	got := m.descriptionBlock(f, 4)
	if strings.Contains(got, "…") {
		t.Errorf("description truncated:\n%s", got)
	}
	if strings.Contains(got, "static") {
		t.Error("Describe should override Description")
	}
	for _, word := range strings.Fields(long) {
		if !strings.Contains(got, word) {
			t.Errorf("word %q dropped from:\n%s", word, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w > m.bodyW() {
			t.Errorf("line width %d > body %d: %q", w, m.bodyW(), line)
		}
	}
	if m.descriptionBlock(&Field{}, 4) != "" {
		t.Error("empty description should render nothing")
	}
}

func TestFormSummaryValues(t *testing.T) {
	m := FormModel{}
	blank := "  "
	no := false
	tests := []struct {
		name  string
		field *Field
		want  string
	}{
		{"blank input", &Field{Kind: FieldInput, Value: &blank}, "—"},
		{"confirm", &Field{Kind: FieldConfirm, BoolValue: &no}, "no"},
		{
			"multiselect keeps option order, not selection order",
			&Field{Kind: FieldMultiSelect, Options: []string{"a", "b", "c"}, Selected: map[string]bool{"c": true, "a": true}},
			"a, c",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := m.summaryValue(tt.field); got != tt.want {
				t.Errorf("summaryValue = %q, want %q", got, tt.want)
			}
		})
	}
}
