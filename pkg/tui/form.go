// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tui provides custom bubbletea form components replacing huh.
package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
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
	FieldGroup
)

// maxFormSuggestions caps the completion list under an input — it renders
// above the footer, so an unbounded list pushes the step stack off screen.
const maxFormSuggestions = 5

// Field represents a single form field.
type Field struct {
	Kind        FieldKind
	Title       string
	Description string
	Value       *string         // for Input
	BoolValue   *bool           // for Confirm
	Options     []string        // for MultiSelect
	Selected    map[string]bool // for MultiSelect
	// Subs are a FieldGroup's fields, answered in order on one screen. Any kind
	// is allowed — a group is a screen, not an input list.
	Subs []*Field
	// Example renders what the option under the cursor would produce, in its own
	// block above the footer. The description explains the question; the example
	// demonstrates the one answer being considered.
	Example func() string
	// Await holds the field while it returns a non-empty string: the text shows
	// under the title and enter refuses to advance.
	Await       func() string
	Placeholder string
	// Suggest returns completions for the typed text. Called per keystroke, so
	// keep it fast. ↑/↓ pick, tab/enter fill.
	Suggest   func(typed string) []string
	Validator func(string) error
	Error     string
}

// DownloadMsg reports the progress of a download running behind the form (the
// location database, fetched while the user answers everything above the town
// fields). The form draws it as one row under the banner and drops that row
// once Finished arrives — a dependency already on disk is never mentioned.
type DownloadMsg struct {
	Label       string
	Done, Total int64
	Finished    bool
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
	subIdx      int      // focused sub-field inside a FieldGroup
	sugg        []string // live completions for the current input field
	suggCursor  int      // ↑/↓-picked suggestion; -1 = none picked

	dl     progress.Model // background-download bar, drawn under the banner
	dlMsg  DownloadMsg
	dlSeen bool // a DownloadMsg arrived; nothing renders before the first one
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
		dl:         progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage()),
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

// inputFocused reports whether keystrokes are going into a text field, where
// letters and digits are ordinary input rather than shortcuts.
func (m FormModel) inputFocused() bool {
	f := m.active()
	return f != nil && f.Kind == FieldInput
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
		case "c":
			// save & exit: same key as the review TUI, for changing one setting
			// without clicking through every step
			if !m.inputFocused() {
				return m.saveAndExit()
			}
		case "enter":
			// an arrowed-onto suggestion: pick it instead of advancing
			if m.suggCursor >= 0 && m.suggCursor < len(m.sugg) && m.inputFocused() {
				m.fillSuggestion(m.suggCursor)
				return m, nil
			}
			return m.moveNext()
		case "shift+tab":
			return m.movePrev()
		case "tab":
			// fill the picked (or top) completion
			if len(m.sugg) > 0 && m.inputFocused() {
				m.fillSuggestion(max(m.suggCursor, 0))
				return m, nil
			}
		case "up", "down":
			// under an input, ↑/↓ walk the suggestion list; otherwise they fall
			// through to the multiselect handling below
			if m.inputFocused() && len(m.sugg) > 0 {
				if msg.String() == "down" && m.suggCursor < len(m.sugg)-1 && m.suggCursor < maxFormSuggestions-1 {
					m.suggCursor++
				} else if msg.String() == "up" && m.suggCursor > -1 {
					m.suggCursor--
				}
				return m, nil
			}
		}

		// digits jump to the step with that number in the stack
		if !m.inputFocused() {
			if n, ok := optionNumber(msg.String(), len(m.Fields)); ok {
				return m.jumpTo(n)
			}
		}

		// Handle field-specific keys. active() (not Fields[Current]) so a
		// confirm or multiselect nested in a FieldGroup answers the same way as
		// a top-level one.
		if field := m.active(); field != nil {
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
				// "yes" renders above "no" — both arrow pairs must match.
				case "up", "k", "left", "h":
					if field.BoolValue != nil {
						*field.BoolValue = true
					}
					return m, nil
				case "down", "j", "right", "l":
					if field.BoolValue != nil {
						*field.BoolValue = false
					}
					return m, nil
				}
			}
		}

	case DownloadMsg:
		m.dlMsg, m.dlSeen = msg, true
		return m, nil

	case tea.WindowSizeMsg:
		m.w = msg.Width
		m.h = msg.Height
		m.dl.Width = clamp(msg.Width/3, 10, 40)
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
			rows = append(rows, m.expandedField(f, i))
		default:
			rows = append(rows, m.collapsedRow(f, i, i < m.Current))
		}
	}
	body := Banner("config") + "\n" + m.downloadRow() + "\n" + strings.Join(rows, "\n")
	footer := m.exampleBlock() + m.renderFooter()

	// ponytail: tiny terminals just lose the top of the stack (banner first);
	// add a scroll window around the current field if that ever matters.
	if m.h > 0 {
		if lines := strings.Split(body, "\n"); len(lines) > m.h-lipgloss.Height(footer)-1 {
			body = strings.Join(lines[len(lines)-(m.h-lipgloss.Height(footer)-1):], "\n")
		}
	}
	return Screen(body, footer, m.h)
}

// downloadRow renders the background download under the banner: a labelled bar
// while it runs, nothing at all before the first report or after it finishes.
// It's the whole reason the form doesn't need an install screen — the download
// is visible without owning the screen.
func (m FormModel) downloadRow() string {
	if !m.dlSeen || m.dlMsg.Finished {
		return ""
	}
	pct := 0.0
	if m.dlMsg.Total > 0 {
		pct = float64(m.dlMsg.Done) / float64(m.dlMsg.Total)
	}
	left := FaintTxt.Render(" ⬇ ") + DimText.Render(m.dlMsg.Label) + "  " +
		m.dl.ViewAs(pct) + "  " + DimText.Render(fmt.Sprintf("%3.0f%%", pct*100))
	return row(left, "", m.w) + "\n"
}

// exampleBlock renders the active field's example — what the option under the
// cursor actually produces — pinned above the footer, the same place the scan
// screen puts its warnings. Keeping it out of the description is what lets the
// description explain the question once while the example shows only the
// choice being considered, instead of listing every option's outcome at once.
func (m FormModel) exampleBlock() string {
	f := m.active()
	if f == nil || f.Example == nil {
		return ""
	}
	ex := strings.TrimSpace(f.Example())
	if ex == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(FaintTxt.Render(" example") + "\n")
	for line := range strings.SplitSeq(ex, "\n") {
		b.WriteString(row("   "+DimText.Render(line), "", m.w))
		b.WriteString("\n")
	}
	return b.String()
}

// awaitReason reports why the current step can't be answered yet ("" = it can).
// A group's Await holds the whole step, not just the sub-field under the
// cursor — its members are one screen and one question.
func (m FormModel) awaitReason() string {
	if m.Current >= len(m.Fields) {
		return ""
	}
	if f := m.Fields[m.Current]; f.Await != nil {
		if r := f.Await(); r != "" {
			return r
		}
	}
	if f := m.active(); f != nil && f.Await != nil {
		return f.Await()
	}
	return ""
}

// optionNumber maps a "1".."9" keypress to a zero-based option index, false if
// the key isn't a digit or points past the last option.
func optionNumber(key string, count int) (int, bool) {
	if len(key) != 1 || key[0] < '1' || key[0] > '9' {
		return 0, false
	}
	n, _ := strconv.Atoi(key)
	if n > count {
		return 0, false
	}
	return n - 1, true
}

// collapsedRow renders a one-line summary of a step: done steps show their
// answer, pending steps are dim placeholders. The leading number is the
// step's jump target — press it from anywhere to land here.
func (m FormModel) collapsedRow(f *Field, i int, done bool) string {
	num := fmt.Sprintf("%d) ", i+1)
	if !done {
		return row(FaintTxt.Render(num+f.Title), "", m.w)
	}
	left := OK.Render(num) + Text.Render(f.Title)
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

// expandedField renders the current step in place: numbered + bold title,
// indented description, then its control (input / yes-no / options).
func (m FormModel) expandedField(f *Field, i int) string {
	var b strings.Builder
	num := fmt.Sprintf("%d) ", i+1)
	b.WriteString(row(Title.Render(num)+Text.Bold(true).Render(f.Title), "", m.w))
	for line := range strings.SplitSeq(f.Description, "\n") {
		if line != "" {
			b.WriteString("\n" + row("    "+DimText.Render(line), "", m.w))
		}
	}

	// A field waiting on something (the location DB download) says so and shows
	// no control: there is nothing useful to answer yet.
	if reason := m.awaitReason(); reason != "" {
		b.WriteString("\n" + row("    "+Attn.Render("⏳ "+reason), "", m.w))
		return b.String()
	}

	if f.Kind == FieldGroup {
		for i, sub := range f.Subs {
			b.WriteString(m.subView(sub, i == m.subIdx))
		}
		return b.String()
	}
	b.WriteString(m.controlView(f, ""))
	return b.String()
}

// subView renders one member of a FieldGroup: the focused one gets its full
// control (and description, since a group's members ask their own questions),
// the rest collapse to a `Title: answer` line.
func (m FormModel) subView(sub *Field, focused bool) string {
	if !focused {
		return "\n    " + Text.Render(sub.Title+": ") + DimText.Render(m.summaryValue(sub))
	}
	var b strings.Builder
	if sub.Kind != FieldInput {
		b.WriteString("\n" + row("    "+Text.Bold(true).Render(sub.Title), "", m.w))
		for line := range strings.SplitSeq(sub.Description, "\n") {
			if line != "" {
				b.WriteString("\n" + row("      "+DimText.Render(line), "", m.w))
			}
		}
	}
	b.WriteString(m.controlView(sub, Text.Render(sub.Title+": ")))
	return b.String()
}

// controlView renders a field's interactive part — the text input, the
// numbered yes/no, or the numbered option list.
func (m FormModel) controlView(f *Field, label string) string {
	var b strings.Builder
	switch f.Kind {
	case FieldInput:
		b.WriteString(m.inputView(f, label))
	case FieldConfirm:
		on := f.BoolValue != nil && *f.BoolValue
		b.WriteString("\n" + optionRow("yes", on, on))
		b.WriteString("\n" + optionRow("no", !on, !on))
	case FieldMultiSelect:
		for i, opt := range f.Options {
			b.WriteString("\n" + optionRow(opt, f.Selected[opt], i == m.multiCursor))
		}
	}
	return b.String()
}

// optionRow renders one pickable option as `marker label`. Options are
// navigated with ↑/↓ (and space/y/n to pick), never numbered — the numbers on
// screen belong to the step list, so jumping between questions and picking an
// option never compete for the same keys.
func optionRow(label string, on, cursor bool) string {
	marker := FaintTxt.Render("○ ")
	if on {
		marker = OK.Render("● ")
	}
	line := marker + Text.Render(label)
	if cursor {
		line = Selected.Render(ansi.Strip(line))
	}
	return "      " + line
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
		if i >= maxFormSuggestions {
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
	if m.awaitReason() != "" {
		return Footer(KeyHint("ctrl+c", "quit")+"   "+KeyHint("c", "save & exit"), m.w)
	}
	field := m.active()
	if field == nil {
		return ""
	}
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
			KeyHint("y/n", "answer"),
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
	if field.Kind != FieldInput {
		hints = append(hints, KeyHint(fmt.Sprintf("1-%d", len(m.Fields)), "jump"))
	}
	hints = append(hints, KeyHint("c", "save & exit"))
	return Footer(strings.Join(hints, "   "), m.w)
}

func (m FormModel) moveNext() (tea.Model, tea.Cmd) {
	// Held field (the town step while the location DB downloads): the screen
	// already says why, and the download row above shows how far along it is.
	if m.awaitReason() != "" {
		return m, nil
	}

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

// jumpTo moves straight to step n (0-based) — the numbered step list is what
// makes this navigable, not the options inside a step. A no-op onto the
// current step or an out-of-range number.
func (m FormModel) jumpTo(n int) (tea.Model, tea.Cmd) {
	if n < 0 || n >= len(m.Fields) || n == m.Current {
		return m, nil
	}
	m.Current = n
	m.subIdx = 0
	m.multiCursor = 0
	m.seedInput()
	return m, textinput.Blink
}

// saveAndExit commits the active input (if any) and submits the form right
// away, without requiring every step to be visited — the escape hatch for a
// user who opened the wizard just to change one setting. A held step (the
// background download) or a failing validator on the field being typed into
// still blocks, same as moveNext.
func (m FormModel) saveAndExit() (tea.Model, tea.Cmd) {
	if m.awaitReason() != "" {
		return m, nil
	}
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
	if m.onSubmit != nil {
		if err := m.onSubmit(); err != nil {
			m.err = err
			return m, tea.Quit
		}
	}
	return m, tea.Quit
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
