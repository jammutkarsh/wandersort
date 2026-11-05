package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// suggestDebounce delays Suggest (a geonames query) until typing pauses.
const suggestDebounce = 50 * time.Millisecond

// suggestDebounceMsg fires after a pause in typing; stale if a later
// keystroke has since bumped suggGen.
type suggestDebounceMsg struct {
	gen   int
	typed string
}

// suggestResultMsg carries a completed Suggest() call back from its tea.Cmd
// goroutine, so the query itself never blocks the render loop.
type suggestResultMsg struct {
	gen     int
	results []string
}

type FieldKind int

const (
	FieldInput FieldKind = iota
	FieldConfirm
	FieldMultiSelect
	FieldGroup
	// FieldSelect picks one of Options into Value; ↑/↓ or a click moves the pick.
	FieldSelect
)

// maxFormSuggestions caps the completion list under an input — it renders
// above the footer, so an unbounded list pushes the step stack off screen.
const maxFormSuggestions = 5

type Field struct {
	Value     *string         // for Input
	BoolValue *bool           // for Confirm
	Selected  map[string]bool // for MultiSelect
	// Skip leaves the step out while it reports true (a follow-up question
	// whose answer doesn't matter yet).
	Skip func() bool
	// Describe overrides Description when the explanation depends on the
	// option under the cursor. Prose goes here, not in Example (which truncates).
	Describe func() string
	// Example renders what the option under the cursor would produce.
	Example func() string
	// Suggest returns completions for the typed text. Called per keystroke, so
	// keep it fast. ↑/↓ pick, tab/enter fill.
	Suggest     func(typed string) []string
	Validator   func(string) error
	Title       string
	Description string
	Recommended string // for Select: the option marked "(recommended)"
	Placeholder string
	Error       string
	Options     []string // for MultiSelect and Select
	// Subs are a FieldGroup's fields, answered in order on one screen. Any kind
	// is allowed — a group is a screen, not an input list.
	Subs []*Field
	Kind FieldKind
}

// FormModel is a multi-step form navigator using bubbletea.
type FormModel struct {
	err      error
	onSubmit func() error
	// Then, when set, is what the form returns instead of Leave once it ends,
	// for a host screen that keeps going afterwards.
	Then func(Leave) tea.Cmd

	// Heading names the form above its steps, with a step count beside it.
	Heading     string
	Fields      []*Field
	sugg        []string // live completions for the current input field
	ti          textinput.Model
	Current     int // current field index
	w, h        int
	multiCursor int // cursor for multiselect field
	subIdx      int // focused sub-field inside a FieldGroup
	suggCursor  int // ↑/↓-picked suggestion; -1 = none picked
	suggGen     int // bumped per keystroke; invalidates in-flight debounce/query

	aborted bool

	// done is set once the form has handed back; the host takes it away on the
	// next message, and keys landing before then are ignored
	done bool

	// quitReq marks the ending key as "done with the app" (ctrl+c) rather than
	// "done here"; it rides out on Leave
	quitReq bool

	// EscSaves makes esc save and leave without asking, for a one-setting
	// edit where what's on screen is the answer.
	EscSaves bool

	showKeys bool // the ? overlay is up

	// askExit is [esc]'s question — save what's been entered, or throw it
	// away — raised instead of assuming "save" the moment someone wants out.
	askExit    bool
	exitChoice bool // true = Save, false = Discard; which button is under the cursor
}

// Busy is never true for a form: it holds the keyboard, not a pipeline.
func (m FormModel) Busy() bool { return false }

// finish hands control back to the container with why the form ended. It never
// quits the program.
func (m FormModel) finish() (tea.Model, tea.Cmd) {
	m.done = true
	l := Leave{Quit: m.quitReq, Aborted: m.aborted, Err: m.err}
	if m.Then != nil {
		return m, m.Then(l)
	}
	return m, Left(l)
}

// formKeys is every form key, behind ?.
var formKeys = []KeyGroup{
	{"Answering", []KeyLine{
		{"↑ ↓", "move between choices or questions"},
		{"← →", "answer yes or no"},
		{"space", "tick or untick"},
		{"y n", "answer yes or no and go on"},
		{"tab", "complete what's typed"},
	}},
	{"Moving", []KeyLine{
		{"enter", "next question"},
		{"ctrl+b", "previous question"},
		{"esc", "save or discard, then leave"},
		{"ctrl+c", "leave without saving"},
	}},
}

// skipped reports whether step i is left out right now.
func (m FormModel) skipped(i int) bool {
	return i < len(m.Fields) && m.Fields[i].Skip != nil && m.Fields[i].Skip()
}

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
	m.suggGen++ // invalidate any debounce/query still in flight for the field just left
	f := m.active()
	if f != nil && f.Kind == FieldSelect && f.Value != nil {
		m.multiCursor = max(slices.Index(f.Options, *f.Value), 0)
		*f.Value = f.Options[m.multiCursor]
	}
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
	m.suggGen++ // invalidate any debounce/query still chasing the pre-fill text
	m.refreshSuggestions()
}

func (m FormModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m FormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.done {
			return m, nil
		}
		// The question owns the keyboard until answered — everything below,
		// including a FieldConfirm's own "y"/"n", would otherwise race it.
		if m.askExit {
			return m.answerExitAsk(msg)
		}
		if m.showKeys {
			m.showKeys = false
			return m, nil
		}
		if msg.String() == "?" && (!m.inputFocused() || m.ti.Value() == "") {
			m.showKeys = true
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c":
			m.aborted, m.quitReq = true, true
			return m.finish()
		case "esc":
			if m.EscSaves {
				return m.saveAndExit()
			}
			// esc works whether or not an input is focused, and asks before
			// leaving
			m.askExit, m.exitChoice = true, true
			return m, nil
		case "enter":
			// an arrowed-onto suggestion: pick it instead of advancing
			if m.suggCursor >= 0 && m.suggCursor < len(m.sugg) && m.inputFocused() {
				m.fillSuggestion(m.suggCursor)
				return m, nil
			}
			// one completion left: enter takes it (the != guard lets the next
			// enter advance). A path already typed in full, with its folders
			// below it offered, is done: enter must not drill into them.
			if m.inputFocused() && len(m.sugg) == 1 && m.sugg[0] != m.ti.Value() && !isBelowTyped(m.sugg[0], m.ti.Value()) {
				m.fillSuggestion(0)
				return m, nil
			}
			return m.moveNext()
		case "ctrl+b":
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
				// Cursor walks the whole list, not just the maxFormSuggestions-tall
				// rendered window — inputView scrolls that window to keep it visible.
				if msg.String() == "down" && m.suggCursor < len(m.sugg)-1 {
					m.suggCursor++
				} else if msg.String() == "up" && m.suggCursor > -1 {
					m.suggCursor--
				}
				return m, nil
			}
		}

		// active(), not Fields[Current], so fields nested in a FieldGroup
		// answer the same keys
		if field := m.active(); field != nil {
			switch field.Kind {
			case FieldSelect:
				key := msg.String()
				switch {
				case key == "up":
					m.multiCursor = max(m.multiCursor-1, 0)
				case key == "down":
					m.multiCursor = min(m.multiCursor+1, len(field.Options)-1)
				default:
					return m, nil
				}
				if field.Value != nil {
					*field.Value = field.Options[m.multiCursor]
				}
				return m, nil
			case FieldMultiSelect:
				switch msg.String() {
				case "up":
					if m.multiCursor > 0 {
						m.multiCursor--
					}
					return m, nil
				case "down":
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
				// ←/→ answer; ↑/↓ only move between a group's questions
				case "left":
					if field.BoolValue != nil {
						*field.BoolValue = true
					}
					return m, nil
				case "right":
					if field.BoolValue != nil {
						*field.BoolValue = false
					}
					return m, nil
				case "up":
					return m.moveWithinGroup(-1)
				case "down":
					return m.moveWithinGroup(1)
				}
			}
		}

	case suggestDebounceMsg:
		// Stale if a keystroke landed during the pause; that keystroke's own
		// debounce is the one that gets to query.
		if msg.gen != m.suggGen {
			return m, nil
		}
		f := m.active()
		if f == nil || f.Kind != FieldInput || f.Suggest == nil {
			return m, nil
		}
		gen, typed := msg.gen, msg.typed
		return m, func() tea.Msg {
			return suggestResultMsg{gen, f.Suggest(typed)}
		}

	case suggestResultMsg:
		if msg.gen == m.suggGen {
			m.sugg = msg.results
			m.suggCursor = -1
		}
		return m, nil

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
		if _, isKey := msg.(tea.KeyMsg); isKey && f.Suggest != nil {
			m.suggGen++
			gen, typed := m.suggGen, m.ti.Value()
			cmd = tea.Batch(cmd, tea.Tick(suggestDebounce, func(time.Time) tea.Msg {
				return suggestDebounceMsg{gen, typed}
			}))
		}
		return m, cmd
	}

	return m, nil
}

func (m *FormModel) refreshSuggestions() {
	m.sugg = nil
	m.suggCursor = -1
	if f := m.active(); f != nil && f.Kind == FieldInput && f.Suggest != nil {
		m.sugg = f.Suggest(m.ti.Value())
	}
}

// View renders the form as a top-down stack: answered fields collapse to one
// row, the current field expands, later fields are dimmed.
func (m FormModel) View() string {
	if m.askExit {
		return m.exitAskView()
	}
	steps := 0
	for i := range m.Fields {
		if !m.skipped(i) {
			steps++
		}
	}
	rows := make([]string, 0, steps+2)
	step, n := 0, 0
	for i, f := range m.Fields {
		if m.skipped(i) {
			continue
		}
		switch {
		case i == m.Current:
			step = n + 1
			rows = append(rows, m.expandedField(f, n, steps))
		default:
			rows = append(rows, m.collapsedRow(f, i, i < m.Current))
		}
		n++
	}
	if m.Heading != "" {
		heading := Text.Bold(true).Render(m.Heading)
		if steps > 1 {
			heading += "  " + FaintTxt.Render(fmt.Sprintf("step %d of %d", step, steps))
		}
		rows = append([]string{row(heading, "", m.bodyW()), ""}, rows...)
	}
	fields := strings.Join(rows, "\n")

	footer := m.detailBlock() + m.renderFooter()
	if m.sidePanel() {
		// wide terminal: the example sits in the otherwise-empty right column,
		// next to the question it belongs to, instead of above the footer
		left := lipgloss.NewStyle().Width(m.bodyW() + 2).Render(fields)
		fields = lipgloss.JoinHorizontal(lipgloss.Top, left, m.examplePanel())
	}
	body := "\n" + fields

	if m.h > 0 {
		if lines := strings.Split(body, "\n"); len(lines) > m.h-lipgloss.Height(footer)-1 {
			body = strings.Join(lines[len(lines)-(m.h-lipgloss.Height(footer)-1):], "\n")
		}
	}
	view := Screen(body, footer, m.h)
	if m.showKeys {
		return KeyHelp(view, formKeys, m.w, m.h)
	}
	return view
}

// formBodyMaxW caps the field stack's width when the side panel is showing.
// examplePanelMinW is the panel's floor width; examplePanelMinTermW is the
// narrowest terminal that shows an example at all, in the side panel.
const (
	formBodyMaxW         = 76
	examplePanelMinW     = 46
	examplePanelMinTermW = 100
)

// sidePanel reports whether the example renders as a right-hand column. Depends
// only on terminal width and whether any field has an example, so the layout
// is stable across steps.
func (m FormModel) sidePanel() bool {
	if m.w < examplePanelMinTermW {
		return false
	}
	for _, f := range m.Fields {
		if f.Example != nil {
			return true
		}
		for _, sub := range f.Subs {
			if sub.Example != nil {
				return true
			}
		}
	}
	return false
}

// bodyW is the field stack's width, capped when the side panel shows.
func (m FormModel) bodyW() int {
	if m.sidePanel() {
		return min(formBodyMaxW, m.w-examplePanelMinW-2)
	}
	return m.w
}

// panelW is the example column's width: whatever the capped body doesn't use.
func (m FormModel) panelW() int {
	return m.w - m.bodyW() - 2
}

// examplePanel renders the active field's example in a bordered box for the
// right column (an empty column when it has none, so fields don't re-wrap).
func (m FormModel) examplePanel() string {
	var ex string
	if f := m.active(); f != nil && f.Example != nil {
		ex = strings.TrimSpace(f.Example())
	}
	if ex == "" {
		return ""
	}
	inner := m.panelW() - 6 // border (2) + padding (4)
	var b strings.Builder
	b.WriteString(FaintTxt.Render("example"))
	for line := range strings.SplitSeq(ex, "\n") {
		b.WriteString("\n")
		b.WriteString(DimText.Render(ansi.Truncate(line, inner, "…")))
	}
	return Box.Width(m.panelW() - 2).Render(b.String())
}

// collapsedRow renders a one-line summary of a step: done steps show their
// answer, pending steps are dim placeholders.
// A done step is clickable: it takes the ctrl+b presses back to that step.
func (m FormModel) collapsedRow(f *Field, field int, done bool) string {
	if !done {
		return row(FaintTxt.Render("○ "+f.Title), "", m.bodyW())
	}
	left := OK.Render("✓ ") + Text.Render(f.Title)
	if v := m.summaryValue(f); v != "" {
		left += "  " + DimText.Render(v)
	}
	return Clickable(row(left, "", m.bodyW()), m.backKeys(field)...)
}

// maxBackPresses bounds backKeys; no form has this many questions.
const maxBackPresses = 32

// backKeys is the ctrl+b presses that bring the form back to field.
func (m FormModel) backKeys(field int) []string {
	if m.done || m.Current >= len(m.Fields) {
		return nil // a finished form is only on screen while the host swaps it out
	}
	var keys []string
	for sim := m; sim.Current > field && len(keys) < maxBackPresses; keys = append(keys, "ctrl+b") {
		next, _ := sim.movePrev()
		sim = next.(FormModel)
	}
	return keys
}

// summaryValue is the collapsed one-line answer for a completed field.
func (m FormModel) summaryValue(f *Field) string {
	switch f.Kind {
	case FieldInput, FieldSelect:
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

// descriptionBlock renders a field's explanation word-wrapped to the body width,
// re-flowing hard line breaks.
func (m FormModel) descriptionBlock(f *Field, indent int) string {
	d := f.Description
	if f.Describe != nil {
		d = f.Describe()
	}
	d = strings.TrimSpace(d)
	if d == "" {
		return ""
	}
	return "\n" + DimText.Width(m.bodyW()).PaddingLeft(indent).Render(d)
}

// expandedField renders the current step in place: bold title, indented
// description, then its control.
func (m FormModel) expandedField(f *Field, i, steps int) string {
	var b strings.Builder
	if f.Kind == FieldGroup {
		// a lone group under a heading is the heading's own screen: its
		// questions are the content, its title would say the heading twice
		if steps > 1 || m.Heading == "" {
			b.WriteString(row(Text.Bold(true).Render(f.Title), "", m.bodyW()))
			b.WriteString(m.descriptionBlock(f, 4))
		}
		for i, sub := range f.Subs {
			b.WriteString(m.subView(sub, i, i == m.subIdx))
		}
		return strings.TrimPrefix(b.String(), "\n")
	}
	b.WriteString(row(Text.Bold(true).Render(f.Title), "", m.bodyW()))
	b.WriteString(m.descriptionBlock(f, 4))
	b.WriteString(m.controlView(f, ""))
	return b.String()
}

// subView renders one FieldGroup member: full control when focused, else a
// `Title: answer` line.
func (m FormModel) subView(sub *Field, idx int, focused bool) string {
	// a click walks to this question: ctrl+b back, enter forward
	key, steps := "enter", idx-m.subIdx
	if steps < 0 {
		key, steps = "ctrl+b", -steps
	}
	walk := slices.Repeat([]string{key}, steps)

	if sub.Kind == FieldConfirm && sub.BoolValue != nil {
		// question on the left, answer buttons on the right; a click on a
		// button walks here, then picks, without moving on. The description
		// of the focused one sits above the footer (detailBlock).
		on := *sub.BoolValue
		buttons := Clickable(choiceButtonText("yes", on), append(slices.Clone(walk), "left")...) + "  " +
			Clickable(choiceButtonText("no", !on), append(slices.Clone(walk), "right")...)
		title := "   " + Text.Render(sub.Title)
		if focused {
			title = "  " + Pill(sub.Title)
		}
		return "\n" + row(Clickable(title, walk...), buttons, m.bodyW())
	}
	if !focused {
		line := "    " + Text.Render(labelled(sub.Title)) + DimText.Render(m.summaryValue(sub))
		return "\n" + Clickable(line, walk...)
	}
	var b strings.Builder
	label := Text.Render(labelled(sub.Title))
	if sub.Kind != FieldInput {
		b.WriteString("\n")
		b.WriteString(row("  "+Pill(sub.Title), "", m.bodyW()))
		b.WriteString(m.descriptionBlock(sub, 6))
		label = ""
	}
	b.WriteString(m.controlView(sub, label))
	return b.String()
}

// detailBlock is the focused yes/no question's description, drawn above the footer.
func (m FormModel) detailBlock() string {
	f := m.active()
	if f == nil || f.Kind != FieldConfirm || m.Fields[m.Current].Kind != FieldGroup {
		return ""
	}
	return m.descriptionBlock(f, 2) + "\n"
}

// labelled is a title with its answer to follow: "Home: ", but a question
// keeps its own mark ("Merge days? ").
func labelled(title string) string {
	if strings.HasSuffix(title, "?") {
		return title + " "
	}
	return title + ": "
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
		b.WriteString("\n      ")
		b.WriteString(choiceButton("yes", on, "left") + "  " + choiceButton("no", !on, "right"))
	case FieldMultiSelect:
		for i, opt := range f.Options {
			b.WriteString("\n")
			b.WriteString(Clickable(optionRow(opt, f.Selected[opt], i == m.multiCursor), append(MoveKeys(m.multiCursor, i), "space")...))
		}
	case FieldSelect:
		for i, opt := range f.Options {
			note := ""
			if opt == f.Recommended {
				note = DimText.Render("  (recommended)")
			}
			b.WriteString("\n")
			pick := append(MoveKeys(m.multiCursor, i), "enter")
			b.WriteString("\n")
			b.WriteString(choiceRow(opt+note, i == m.multiCursor, 4, pick...))
		}
	}
	return b.String()
}

// choiceButton is one side of a yes/no answer, drawn as a button; a click picks
// it without leaving the question, so the example beside it shows the result.
func choiceButton(label string, on bool, key string) string {
	return Clickable(choiceButtonText(label, on), key)
}

// choiceButtonText is a button's look. The label never changes; the picked
// side is filled, bold and underlined, so the pick doesn't rest on colour.
func choiceButtonText(label string, on bool) string {
	cell := lipgloss.NewStyle().Width(choiceButtonW).Align(lipgloss.Center)
	if on {
		return pillStyle.Inherit(cell).Render(label)
	}
	return DimText.Inherit(cell).Render(label)
}

// choiceButtonW is every yes/no button's width, so the right edges line up in every row.
const choiceButtonW = 7

// optionRow renders one option as `marker label`. Options are picked with
// ↑/↓ and space/y/n; on-screen numbers are step ordinals.
func optionRow(label string, on, cursor bool) string {
	marker := FaintTxt.Render("○ ")
	if on {
		marker = OK.Render("● ")
	}
	line := " " + marker + Text.Render(label)
	if cursor {
		line = Pill(marker + label)
	}
	return "     " + line
}

// inputView renders the live textinput plus its error and suggestion list —
// shared by plain input fields and a group's focused sub-input.
func (m FormModel) inputView(f *Field, label string) string {
	var b strings.Builder
	prompt := m.ti.Prompt
	m.ti.Prompt = Prompt()
	b.WriteString("\n    ")
	b.WriteString(label)
	b.WriteString(m.ti.View())
	m.ti.Prompt = prompt
	if f.Error != "" {
		b.WriteString("\n    ")
		b.WriteString(Bad.Render("✗ " + f.Error))
	}
	start, end := m.suggWindow()
	if start > 0 {
		b.WriteString("\n")
		b.WriteString(row("      "+FaintTxt.Render(fmt.Sprintf("↑ %d more", start)), "", m.bodyW()))
	}
	for i := start; i < end; i++ {
		s := m.sugg[i]
		var line string
		switch {
		case i == m.suggCursor:
			line = "     " + Pill(s) + FaintTxt.Render("  ⏎ pick")
		case i == 0 && m.suggCursor < 0:
			line = "      " + FaintTxt.Render("· ") + DimText.Render(s) + FaintTxt.Render("  ⇥ tab")
		default:
			line = "      " + FaintTxt.Render("· ") + DimText.Render(s)
		}
		b.WriteString("\n")
		b.WriteString(row(line, "", m.bodyW()))
	}
	if rest := len(m.sugg) - end; rest > 0 {
		b.WriteString("\n")
		b.WriteString(row("      "+FaintTxt.Render(fmt.Sprintf("↓ %d more", rest)), "", m.bodyW()))
	}
	return b.String()
}

func (m FormModel) suggWindow() (start, end int) {
	return suggWindow(len(m.sugg), m.suggCursor, maxFormSuggestions)
}

// suggWindow returns the [start, end) slice of a completion list to render,
// scrolled to keep cursor visible.
func suggWindow(n, cursor, rows int) (start, end int) {
	if n <= rows {
		return 0, n
	}
	start = max(cursor-rows+1, 0)
	start = min(start, n-rows)
	return start, start + rows
}

func (m FormModel) renderFooter() string {
	if m.Current >= len(m.Fields) {
		return ""
	}
	field := m.active()
	if field == nil {
		return ""
	}
	var hints []string
	switch field.Kind {
	case FieldSelect:
		hints = []string{KeyHint("↑↓", "choose")}
	case FieldMultiSelect:
		hints = []string{KeyHint("space", "on/off")}
	case FieldConfirm:
		hints = []string{KeyHint("←→", "yes or no")}
		if m.Fields[m.Current].Kind == FieldGroup {
			hints = append([]string{KeyHint("↑↓", "question")}, hints...)
		}
	default:
		if len(m.sugg) > 0 {
			hints = []string{KeyHint("tab", "complete")}
		}
	}
	hints = append(hints, KeyHint("enter", "next"))
	esc := KeyHint("esc", "save")
	if !m.EscSaves {
		esc = KeyHint("esc", "done")
		// a one-setting edit leaves with esc; ctrl+b stays behind ?
		if m.Current > 0 || m.subIdx > 0 {
			hints = append(hints, KeyHint("ctrl+b", "previous question"))
		}
	}
	hints = append(hints, esc, MoreKeys())
	return Footer(strings.Join(hints, "   "), m.w)
}

func (m FormModel) moveNext() (tea.Model, tea.Cmd) {
	// Validate the active input (a plain field or a group sub-input).
	if f := m.active(); f != nil && f.Kind == FieldInput {
		if f.Validator != nil {
			if err := f.Validator(m.ti.Value()); err != nil {
				f.Error = Explain(err)
				return m, nil
			}
		}
		f.Error = ""
		if f.Value != nil {
			*f.Value = m.ti.Value()
		}
	}

	if m.Current >= len(m.Fields) {
		return m, nil // a finished form has nowhere to go next
	}
	// Inside a group: step to its next sub-input before leaving the field.
	if cur := m.Fields[m.Current]; cur.Kind == FieldGroup && m.subIdx < len(cur.Subs)-1 {
		m.subIdx++
		m.seedInput()
		return m, textinput.Blink
	}

	m.Current++
	for m.skipped(m.Current) {
		m.Current++
	}
	m.subIdx = 0
	m.multiCursor = 0
	if m.Current >= len(m.Fields) {
		// Form complete, call onSubmit
		if m.onSubmit != nil {
			if err := m.onSubmit(); err != nil {
				m.err = err
				return m.finish()
			}
		}
		return m.finish()
	}
	m.seedInput()
	return m, textinput.Blink
}

// exitAskView is [esc]'s Save/Discard question, drawn as a ConfirmModel.
func (m FormModel) exitAskView() string {
	choice := m.exitChoice
	c := NewConfirmModel("Save your changes?",
		"Keep what you entered, or leave without it.", &choice)
	c.YesLabel, c.NoLabel = "Save", "Discard"
	sized, _ := c.Update(tea.WindowSizeMsg{Width: m.w, Height: m.h})
	return sized.View()
}

// answerExitAsk drives the [esc] modal with ConfirmModel's keys; ctrl+c
// discards.
func (m FormModel) answerExitAsk(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c":
		m.aborted, m.quitReq = true, true
		return m.finish()
	case "left":
		m.exitChoice = true
	case "right":
		m.exitChoice = false
	case "y":
		m.askExit = false
		return m.saveAndExit()
	case "n", "esc":
		m.askExit = false
		m.aborted = true
		return m.finish()
	case "enter":
		m.askExit = false
		if m.exitChoice {
			return m.saveAndExit()
		}
		m.aborted = true
		return m.finish()
	}
	return m, nil
}

// saveAndExit commits the active input and submits the form without visiting
// every step.
func (m FormModel) saveAndExit() (tea.Model, tea.Cmd) {
	if f := m.active(); f != nil && f.Kind == FieldInput {
		if f.Validator != nil {
			if err := f.Validator(m.ti.Value()); err != nil { // a failing validator on the field being typed still blocks
				f.Error = Explain(err)
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
			return m.finish()
		}
	}
	return m.finish()
}

// moveWithinGroup focuses the group's previous (-1) or next (1) question, without answering or leaving.
func (m FormModel) moveWithinGroup(delta int) (tea.Model, tea.Cmd) {
	if m.Current >= len(m.Fields) || m.Fields[m.Current].Kind != FieldGroup {
		return m, nil
	}
	next := m.subIdx + delta
	if next < 0 || next >= len(m.Fields[m.Current].Subs) {
		return m, nil
	}
	m.subIdx = next
	m.seedInput()
	return m, textinput.Blink
}

// isBelowTyped reports whether completion is a path inside the typed one.
func isBelowTyped(completion, typed string) bool {
	typed = strings.TrimRight(strings.TrimSpace(typed), "/")
	return typed != "" && strings.HasPrefix(completion, typed+"/")
}

func (m FormModel) movePrev() (tea.Model, tea.Cmd) {
	if m.Current >= len(m.Fields) {
		return m, nil // a finished form has nowhere to go back to
	}
	// Inside a group: step back through its sub-inputs first.
	if cur := m.Fields[m.Current]; cur.Kind == FieldGroup && m.subIdx > 0 {
		m.subIdx--
		m.seedInput()
		return m, textinput.Blink
	}
	prev := m.Current - 1
	for prev >= 0 && m.skipped(prev) {
		prev--
	}
	if prev >= 0 {
		m.Current = prev
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

type ConfirmModel struct {
	Title       string
	Description string
	Value       *bool
	// YesLabel/NoLabel override the "yes"/"no" button text.
	YesLabel, NoLabel string
	// Keys overrides the footer's "y / n" hints, for callers that drive the
	// modal with other keys.
	Keys string
	w, h int
}

func (m ConfirmModel) yesLabel() string {
	if m.YesLabel != "" {
		return m.YesLabel
	}
	return "yes"
}

func (m ConfirmModel) noLabel() string {
	if m.NoLabel != "" {
		return m.NoLabel
	}
	return "no"
}

func NewConfirmModel(title, description string, value *bool) ConfirmModel {
	return ConfirmModel{
		Title:       title,
		Description: description,
		Value:       value,
	}
}

func (m ConfirmModel) Init() tea.Cmd {
	return nil
}

func (m ConfirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter": // whichever side is highlighted
			return m, tea.Quit
		case "y":
			if m.Value != nil {
				*m.Value = true
			}
			return m, tea.Quit
		case "n", "esc":
			if m.Value != nil {
				*m.Value = false
			}
			return m, tea.Quit
		// "yes" renders on the left, "no" on the right.
		case "left":
			if m.Value != nil {
				*m.Value = true
			}
		case "right":
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

func (m ConfirmModel) View() string {
	// wrapped at a form's body width, inside the padding, so a long
	// explanation folds instead of running off the edge
	w := formBodyMaxW
	if m.w > 0 {
		w = min(w, m.w)
	}
	title := Title.Width(w).Render(m.Title)
	if m.Description != "" {
		title += "\n\n" + DimText.Width(w).Render(m.Description)
	}

	yes, no := m.yesLabel(), m.noLabel()
	var buttons string
	if m.Value != nil && *m.Value {
		buttons = Clickable(OK.Render(yes), "left", "enter") + "  " + Clickable(DimText.Render(no), "esc")
	} else {
		buttons = Clickable(DimText.Render(yes), "left", "enter") + "  " + Clickable(Bad.Render(no), "esc")
	}

	content := title + "\n\n" + buttons
	body := lipgloss.NewStyle().PaddingTop(1).Render(content)
	keys := m.Keys
	if keys == "" {
		// esc answers no too, unlisted
		keys = KeyHint("←→", "choose") + "   " + KeyHint("enter", "confirm")
	}
	return Screen(body, Footer(keys, m.w), m.h)
}

// Ask is a full-screen yes/no before something hard to undo: ←/→ move the
// highlight, which starts on no; enter takes it, sending Msg on yes; esc is no.
type Ask struct {
	Msg           tea.Msg
	Title, Detail string
	Action        string // the yes button's label
	yes           bool   // the highlighted answer
}

// Key answers k: done reports whether the ask is over, cmd what it sends.
func (a *Ask) Key(k tea.KeyMsg) (done bool, cmd tea.Cmd) {
	switch k.String() {
	case "ctrl+c":
		return true, Left(Leave{Quit: true})
	case "left":
		a.yes = true
	case "right":
		a.yes = false
	case "enter":
		if !a.yes {
			return true, nil
		}
		msg := a.Msg
		return true, func() tea.Msg { return msg }
	case "esc":
		return true, nil
	}
	return false, nil
}

func (a Ask) View(w, h int) string {
	yes := a.yes
	cm := NewConfirmModel(a.Title, a.Detail, &yes)
	cm.YesLabel, cm.NoLabel = a.Action, "cancel"
	cm.w, cm.h = w, h
	return cm.View()
}
