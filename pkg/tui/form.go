// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tui provides custom bubbletea form components replacing huh.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// FieldKind defines the type of form field.
type FieldKind int

const (
	FieldInput FieldKind = iota
	FieldConfirm
	FieldMultiSelect
	FieldNote
	FieldGroup
)

// Field represents a single form field.
type Field struct {
	Kind        FieldKind
	Title       string
	Description string
	// Describe overrides Description when set. Re-evaluated on every render,
	// so an example path can react live to the current selection.
	Describe  func() string
	Value     *string         // for Input
	BoolValue *bool           // for Confirm
	Options   []string        // for MultiSelect
	Selected  map[string]bool // for MultiSelect
	// Subs are the labelled inputs of a FieldGroup, answered in order on one
	// screen (e.g. home town + work town).
	Subs        []*Field
	Placeholder string
	// Suggest returns completions for the typed text (called on every
	// keystroke — keep it fast; e.g. a location-DB prefix search). ↑/↓ pick,
	// tab/enter fill.
	Suggest   func(typed string) []string
	Validator func(string) error
	Error     string
}

// FormModel is a multi-step form navigator using bubbletea.
type FormModel struct {
	Fields      []*Field
	Current     int // current field index
	w, h        int
	ti          textinput.Model
	onSubmit    func() error
	err         error
	aborted     bool
	multiCursor int      // cursor for multiselect field
	subIdx      int      // focused sub-input inside a FieldGroup
	sugg        []string // live completions for the current input field
	suggCursor  int      // ↑/↓-picked suggestion; -1 = none picked
}

// NewFormModel creates a form with the given fields.
func NewFormModel(fields []*Field, onSubmit func() error) FormModel {
	ti := textinput.New()
	ti.Focus()
	m := FormModel{
		Fields:     fields,
		ti:         ti,
		onSubmit:   onSubmit,
		suggCursor: -1,
	}
	m.seedInput()
	return m
}

// active returns the leaf field keyboard input targets: the current field, or
// the focused sub-input of a FieldGroup.
func (m FormModel) active() *Field {
	if m.Current >= len(m.Fields) {
		return nil
	}
	f := m.Fields[m.Current]
	if f.Kind == FieldGroup && m.subIdx < len(f.Subs) {
		return f.Subs[m.subIdx]
	}
	return f
}

func (m FormModel) activeKind() FieldKind {
	if f := m.active(); f != nil {
		return f.Kind
	}
	return FieldNote
}

// seedInput points the shared textinput at the active field (no-op controls
// just get a cleared input) — called on every field/sub-field transition.
func (m *FormModel) seedInput() {
	m.ti.Reset()
	m.sugg = nil
	m.suggCursor = -1
	f := m.active()
	if f == nil || f.Kind != FieldInput {
		return
	}
	if f.Value != nil {
		m.ti.SetValue(*f.Value)
	}
	m.ti.Placeholder = f.Placeholder
	m.ti.CursorEnd()
	m.refreshSuggestions()
}

// fillSuggestion writes the picked completion into the input.
func (m *FormModel) fillSuggestion(i int) {
	m.ti.SetValue(m.sugg[i])
	m.ti.CursorEnd()
	if f := m.active(); f != nil && f.Value != nil {
		*f.Value = m.ti.Value()
	}
	m.refreshSuggestions()
}

// Init implements tea.Model.
func (m FormModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update implements tea.Model.
func (m FormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.aborted = true
			return m, tea.Quit
		case "enter":
			// An arrowed-onto suggestion: enter picks it instead of advancing.
			if m.suggCursor >= 0 && m.suggCursor < len(m.sugg) && m.activeKind() == FieldInput {
				m.fillSuggestion(m.suggCursor)
				return m, nil
			}
			return m.moveNext()
		case "shift+tab":
			return m.movePrev()
		case "tab":
			// Fill the picked (or top) completion.
			if len(m.sugg) > 0 && m.activeKind() == FieldInput {
				m.fillSuggestion(max(m.suggCursor, 0))
				return m, nil
			}
		case "up", "down":
			// Navigate the suggestion list under an input. Falls through to the
			// multiselect's own ↑/↓ handling below for that kind.
			if m.activeKind() == FieldInput && len(m.sugg) > 0 {
				if msg.String() == "down" && m.suggCursor < len(m.sugg)-1 && m.suggCursor < 4 {
					m.suggCursor++
				} else if msg.String() == "up" && m.suggCursor > -1 {
					m.suggCursor--
				}
				return m, nil
			}
		}

		// Handle field-specific keys
		if m.Current < len(m.Fields) {
			field := m.Fields[m.Current]
			switch field.Kind {
			case FieldMultiSelect:
				switch msg.String() {
				case "up", "k":
					if m.multiCursor > 0 {
						m.multiCursor--
					}
					return m, nil
				case "down", "j":
					if m.multiCursor < len(field.Options)-1 {
						m.multiCursor++
					}
					return m, nil
				case " ":
					if m.multiCursor < len(field.Options) {
						opt := field.Options[m.multiCursor]
						field.Selected[opt] = !field.Selected[opt]
					}
					return m, nil
				}
			case FieldConfirm:
				switch msg.String() {
				case "y":
					if field.BoolValue != nil {
						*field.BoolValue = true
					}
					return m.moveNext()
				case "n":
					if field.BoolValue != nil {
						*field.BoolValue = false
					}
					return m.moveNext()
				// "yes" renders on the left, "no" on the right — arrows must
				// match what's on screen.
				case "left", "h":
					if field.BoolValue != nil {
						*field.BoolValue = true
					}
					return m, nil
				case "right", "l":
					if field.BoolValue != nil {
						*field.BoolValue = false
					}
					return m, nil
				}
			}
		}

	case tea.WindowSizeMsg:
		m.w = msg.Width
		m.h = msg.Height
	}

	// Update the active input (a plain field or a group's focused sub-input).
	if f := m.active(); f != nil && f.Kind == FieldInput {
		var cmd tea.Cmd
		m.ti, cmd = m.ti.Update(msg)
		if f.Value != nil {
			*f.Value = m.ti.Value()
		}
		if _, isKey := msg.(tea.KeyMsg); isKey {
			m.refreshSuggestions()
		}
		return m, cmd
	}

	return m, nil
}

// refreshSuggestions recomputes the completion list for the active input.
func (m *FormModel) refreshSuggestions() {
	m.sugg = nil
	m.suggCursor = -1
	if f := m.active(); f != nil && f.Kind == FieldInput && f.Suggest != nil {
		m.sugg = f.Suggest(m.ti.Value())
	}
}

// View renders the whole form as a top-down stack, same visual language as the
// scan screen's StageList: answered fields collapse to one ` => Title  value`
// row, the current field expands in place with its description and control,
// fields still to come sit dimmed below. The stack itself is the progress
// indicator — no step counter needed.
func (m FormModel) View() string {
	rows := make([]string, 0, len(m.Fields))
	for i, f := range m.Fields {
		switch {
		case i == m.Current:
			rows = append(rows, m.expandedField(f))
		default:
			rows = append(rows, m.collapsedRow(f, i < m.Current))
		}
	}
	body := Banner("setup") + "\n\n" + strings.Join(rows, "\n")
	footer := m.renderFooter()

	// ponytail: tiny terminals just lose the top of the stack (banner first);
	// add a scroll window around the current field if that ever matters.
	if m.h > 0 {
		if lines := strings.Split(body, "\n"); len(lines) > m.h-lipgloss.Height(footer)-1 {
			body = strings.Join(lines[len(lines)-(m.h-lipgloss.Height(footer)-1):], "\n")
		}
	}
	return Screen(body, footer, m.h)
}

// collapsedRow renders a one-line summary of a field: done fields show their
// answer, pending fields are dim placeholders.
func (m FormModel) collapsedRow(f *Field, done bool) string {
	if !done {
		return row(FaintTxt.Render(" => "+f.Title), "", m.w)
	}
	left := OK.Render(" => ") + Text.Render(f.Title)
	if v := m.summaryValue(f); v != "" {
		left += "  " + DimText.Render(v)
	}
	return row(left, "", m.w)
}

// summaryValue is the collapsed one-line answer for a completed field.
func (m FormModel) summaryValue(f *Field) string {
	switch f.Kind {
	case FieldInput:
		if f.Value == nil || strings.TrimSpace(*f.Value) == "" {
			return "—"
		}
		return strings.TrimSpace(*f.Value)
	case FieldConfirm:
		if f.BoolValue != nil && *f.BoolValue {
			return "yes"
		}
		return "no"
	case FieldMultiSelect:
		var sel []string
		for _, opt := range f.Options {
			if f.Selected[opt] {
				sel = append(sel, opt)
			}
		}
		if len(sel) == 0 {
			return "none"
		}
		return strings.Join(sel, ", ")
	case FieldGroup:
		var parts []string
		for _, sub := range f.Subs {
			if sub.Value != nil && strings.TrimSpace(*sub.Value) != "" {
				parts = append(parts, strings.TrimSpace(*sub.Value))
			}
		}
		if len(parts) == 0 {
			return "—"
		}
		return strings.Join(parts, " / ")
	}
	return "" // FieldNote
}

// expandedField renders the current field in place: highlighted marker + bold
// title, indented description, then its control (input / yes-no / options).
func (m FormModel) expandedField(f *Field) string {
	var b strings.Builder
	b.WriteString(row(Title.Render(" => ")+Text.Bold(true).Render(f.Title), "", m.w))
	desc := f.Description
	if f.Describe != nil {
		desc = f.Describe()
	}
	for line := range strings.SplitSeq(desc, "\n") {
		if line != "" {
			b.WriteString("\n" + row("    "+DimText.Render(line), "", m.w))
		}
	}

	switch f.Kind {
	case FieldInput:
		b.WriteString(m.inputView(f, ""))
	case FieldGroup:
		for i, sub := range f.Subs {
			label := Text.Render(sub.Title + ": ")
			if i == m.subIdx {
				b.WriteString(m.inputView(sub, label))
				continue
			}
			val := ""
			if sub.Value != nil {
				val = strings.TrimSpace(*sub.Value)
			}
			shown := DimText.Render(val)
			if val == "" {
				shown = FaintTxt.Render(sub.Placeholder)
			}
			b.WriteString("\n    " + label + shown)
		}
	case FieldConfirm:
		if f.BoolValue != nil && *f.BoolValue {
			b.WriteString("\n    " + OK.Render("● yes") + "  " + FaintTxt.Render("○ no"))
		} else {
			b.WriteString("\n    " + FaintTxt.Render("○ yes") + "  " + Attn.Render("● no"))
		}
	case FieldMultiSelect:
		for i, opt := range f.Options {
			var line string
			if f.Selected[opt] {
				line = OK.Render("✓ ") + Text.Render(opt)
			} else {
				line = FaintTxt.Render("· ") + Text.Render(opt)
			}
			if i == m.multiCursor {
				line = Selected.Render(ansi.Strip(line))
			}
			b.WriteString("\n    " + line)
		}
	}
	return b.String()
}

// inputView renders the live textinput plus its error and suggestion list —
// shared by plain input fields and a group's focused sub-input.
func (m FormModel) inputView(f *Field, label string) string {
	var b strings.Builder
	prompt := m.ti.Prompt
	m.ti.Prompt = lipgloss.NewStyle().Foreground(Primary).Render("» ")
	b.WriteString("\n    " + label + m.ti.View())
	m.ti.Prompt = prompt
	if f.Error != "" {
		b.WriteString("\n    " + Bad.Render("✗ "+f.Error))
	}
	for i, s := range m.sugg {
		if i >= 5 {
			break
		}
		var line string
		switch {
		case i == m.suggCursor:
			line = "      " + Selected.Render(s) + FaintTxt.Render("  ⏎ pick")
		case i == 0 && m.suggCursor < 0:
			line = "      " + FaintTxt.Render("· ") + DimText.Render(s) + FaintTxt.Render("  ⇥ tab")
		default:
			line = "      " + FaintTxt.Render("· ") + DimText.Render(s)
		}
		b.WriteString("\n" + row(line, "", m.w))
	}
	return b.String()
}

func (m FormModel) renderFooter() string {
	if m.Current >= len(m.Fields) {
		return ""
	}
	field := m.Fields[m.Current]
	var hints []string
	switch field.Kind {
	case FieldMultiSelect:
		hints = []string{
			KeyHint("↑↓", "navigate"),
			KeyHint("space", "toggle"),
			KeyHint("enter", "next"),
			KeyHint("shift+tab", "back"),
		}
	case FieldConfirm:
		hints = []string{
			KeyHint("y/n", "choose"),
			KeyHint("enter", "next"),
			KeyHint("shift+tab", "back"),
		}
	default:
		hints = []string{
			KeyHint("enter", "next"),
			KeyHint("shift+tab", "back"),
		}
		if len(m.sugg) > 0 {
			hints = append([]string{KeyHint("↑↓", "pick"), KeyHint("tab", "complete")}, hints...)
		}
	}
	return Footer(strings.Join(hints, "   "), m.w)
}

func (m FormModel) moveNext() (tea.Model, tea.Cmd) {
	// Validate the active input (a plain field or a group sub-input).
	if f := m.active(); f != nil && f.Kind == FieldInput {
		if f.Validator != nil {
			if err := f.Validator(m.ti.Value()); err != nil {
				f.Error = err.Error()
				return m, nil
			}
		}
		f.Error = ""
		if f.Value != nil {
			*f.Value = m.ti.Value()
		}
	}

	// Inside a group: step to its next sub-input before leaving the field.
	if cur := m.Fields[m.Current]; cur.Kind == FieldGroup && m.subIdx < len(cur.Subs)-1 {
		m.subIdx++
		m.seedInput()
		return m, textinput.Blink
	}

	m.Current++
	m.subIdx = 0
	m.multiCursor = 0
	if m.Current >= len(m.Fields) {
		// Form complete, call onSubmit
		if m.onSubmit != nil {
			if err := m.onSubmit(); err != nil {
				m.err = err
				return m, tea.Quit
			}
		}
		return m, tea.Quit
	}
	m.seedInput()
	return m, textinput.Blink
}

func (m FormModel) movePrev() (tea.Model, tea.Cmd) {
	// Inside a group: step back through its sub-inputs first.
	if cur := m.Fields[m.Current]; cur.Kind == FieldGroup && m.subIdx > 0 {
		m.subIdx--
		m.seedInput()
		return m, textinput.Blink
	}
	if m.Current > 0 {
		m.Current--
		m.multiCursor = 0
		m.subIdx = 0
		// Backing into a group lands on its last sub-input.
		if prev := m.Fields[m.Current]; prev.Kind == FieldGroup {
			m.subIdx = len(prev.Subs) - 1
		}
		m.seedInput()
	}
	return m, textinput.Blink
}

// IsAborted returns true if the user cancelled the form.
func (m FormModel) IsAborted() bool {
	return m.aborted
}

// Error returns any error that occurred during submission.
func (m FormModel) Error() error {
	return m.err
}

// ConfirmModel is a simple yes/no dialog.
type ConfirmModel struct {
	Title       string
	Description string
	Value       *bool
	w, h        int
	aborted     bool
}

// NewConfirmModel creates a confirmation dialog.
func NewConfirmModel(title, description string, value *bool) ConfirmModel {
	return ConfirmModel{
		Title:       title,
		Description: description,
		Value:       value,
	}
}

// Init implements tea.Model.
func (m ConfirmModel) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (m ConfirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.aborted = true
			return m, tea.Quit
		case "y", "enter":
			if m.Value != nil {
				*m.Value = true
			}
			return m, tea.Quit
		case "n", "escape":
			if m.Value != nil {
				*m.Value = false
			}
			return m, tea.Quit
		// "yes" renders on the left, "no" on the right.
		case "left", "h":
			if m.Value != nil {
				*m.Value = true
			}
		case "right", "l":
			if m.Value != nil {
				*m.Value = false
			}
		}
	case tea.WindowSizeMsg:
		m.w = msg.Width
		m.h = msg.Height
	}
	return m, nil
}

// View implements tea.Model.
func (m ConfirmModel) View() string {
	title := Title.Render(m.Title)
	if m.Description != "" {
		title += "\n\n" + DimText.Render(m.Description)
	}

	var buttons string
	if m.Value != nil && *m.Value {
		buttons = OK.Render("yes") + "  " + DimText.Render("no")
	} else {
		buttons = DimText.Render("yes") + "  " + Bad.Render("no")
	}

	content := title + "\n\n" + buttons
	body := lipgloss.NewStyle().Padding(1).Render(content)
	footer := Footer(fmt.Sprintf("%s / %s", KeyHint("y", "yes"), KeyHint("n", "no")), m.w)
	return Screen(body, footer, m.h)
}

// IsAborted returns true if the user cancelled the dialog.
func (m ConfirmModel) IsAborted() bool {
	return m.aborted
}
