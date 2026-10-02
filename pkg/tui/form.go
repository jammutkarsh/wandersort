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
	// FieldSelect picks one of Options into Value; options are numbered and a
	// digit picks one.
	FieldSelect
)

// maxFormSuggestions caps the completion list under an input — it renders
// above the footer, so an unbounded list pushes the step stack off screen.
const maxFormSuggestions = 5

type Field struct {
	Kind        FieldKind
	Title       string
	Description string
	Value       *string         // for Input
	BoolValue   *bool           // for Confirm
	Options     []string        // for MultiSelect and Select
	Selected    map[string]bool // for MultiSelect
	// Skip leaves the step out while it reports true (a follow-up question
	// whose answer doesn't matter yet).
	Skip func() bool
	// Subs are a FieldGroup's fields, answered in order on one screen. Any kind
	// is allowed — a group is a screen, not an input list.
	Subs []*Field
	// Describe overrides Description when the explanation depends on the
	// option under the cursor. Prose goes here, not in Example (which truncates).
	Describe func() string
	// Example renders what the option under the cursor would produce.
	Example     func() string
	Placeholder string
	// Suggest returns completions for the typed text. Called per keystroke, so
	// keep it fast. ↑/↓ pick, tab/enter fill.
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
	subIdx      int      // focused sub-field inside a FieldGroup
	sugg        []string // live completions for the current input field
	suggCursor  int      // ↑/↓-picked suggestion; -1 = none picked
	suggGen     int      // bumped per keystroke; invalidates in-flight debounce/query

	// quitReq marks the ending key as "done with the app" (ctrl+c) rather than
	// "done here"; it rides out on Leave
	quitReq bool

	// Heading names the form above its steps, with a step count beside it.
	Heading string
	// Then, when set, is what the form returns instead of Leave once it ends,
	// for a host screen that keeps going afterwards.
	Then func(Leave) tea.Cmd
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
	l := Leave{Quit: m.quitReq, Aborted: m.aborted, Err: m.err}
	if m.Then != nil {
		return m, m.Then(l)
	}
	return m, Left(l)
}

// formKeys is every form key, behind ?.
var formKeys = []KeyGroup{
	{"Answering", []KeyLine{
		{"↑ ↓", "move between choices"},
		{"1-9", "pick a numbered choice"},
		{"space", "tick or untick"},
		{"y n", "answer yes or no"},
		{"tab", "complete what's typed"},
	}},
	{"Moving", []KeyLine{
		{"enter", "next step"},
		{"ctrl+b", "previous step"},
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
			// enter advance)
			if m.inputFocused() && len(m.sugg) == 1 && m.sugg[0] != m.ti.Value() {
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
				case len(key) == 1 && key[0] >= '1' && int(key[0]-'0') <= len(field.Options):
					m.multiCursor = int(key[0] - '1')
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
				// "yes" renders above "no" — both arrow pairs must match.
				case "up", "left":
					if field.BoolValue != nil {
						*field.BoolValue = true
					}
					return m, nil
				case "down", "right":
					if field.BoolValue != nil {
						*field.BoolValue = false
					}
					return m, nil
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
	rows := make([]string, 0, len(m.Fields)+2)
	step, steps := 0, 0
	for i, f := range m.Fields {
		if m.skipped(i) {
			continue
		}
		switch {
		case i == m.Current:
			step = steps + 1
			rows = append(rows, m.expandedField(f, steps))
		default:
			rows = append(rows, m.collapsedRow(f, steps, i < m.Current))
		}
		steps++
	}
	if m.Heading != "" {
		count := ""
		if steps > 1 {
			count = fmt.Sprintf("step %d of %d", step, steps)
		}
		rows = append([]string{row(Text.Bold(true).Render(m.Heading), FaintTxt.Render(count), m.bodyW()), ""}, rows...)
	}
	fields := strings.Join(rows, "\n")

	footer := m.renderFooter()
	if m.sidePanel() {
		// wide terminal: the example sits in the otherwise-empty right column,
		// next to the question it belongs to, instead of above the footer
		left := lipgloss.NewStyle().Width(m.bodyW() + 2).Render(fields)
		fields = lipgloss.JoinHorizontal(lipgloss.Top, left, m.examplePanel())
	} else {
		footer = m.exampleBlock() + footer
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
// narrowest terminal that gets a side panel at all (else exampleBlock).
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

// exampleBlock renders the active field's example above the footer (narrow
// terminals).
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
	b.WriteString(FaintTxt.Render(" example"))
	b.WriteString("\n")
	for line := range strings.SplitSeq(ex, "\n") {
		b.WriteString(row("   "+DimText.Render(line), "", m.w))
		b.WriteString("\n")
	}
	return b.String()
}

// collapsedRow renders a one-line summary of a step: done steps show their
// answer, pending steps are dim placeholders.
func (m FormModel) collapsedRow(f *Field, i int, done bool) string {
	num := fmt.Sprintf("%d) ", i+1)
	if !done {
		return row(FaintTxt.Render(num+f.Title), "", m.bodyW())
	}
	left := OK.Render(num) + Text.Render(f.Title)
	if v := m.summaryValue(f); v != "" {
		left += "  " + DimText.Render(v)
	}
	return row(left, "", m.bodyW())
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
	d = strings.TrimSpace(strings.ReplaceAll(d, "\n", " "))
	if d == "" {
		return ""
	}
	return "\n" + DimText.Width(m.bodyW()).PaddingLeft(indent).Render(d)
}

// expandedField renders the current step in place: numbered + bold title,
// indented description, then its control (input / yes-no / options).
func (m FormModel) expandedField(f *Field, i int) string {
	var b strings.Builder
	num := fmt.Sprintf("%d) ", i+1)
	b.WriteString(row(Title.Render(num)+Text.Bold(true).Render(f.Title), "", m.bodyW()))
	b.WriteString(m.descriptionBlock(f, 4))

	if f.Kind == FieldGroup {
		for i, sub := range f.Subs {
			b.WriteString(m.subView(sub, i, i == m.subIdx))
		}
		return b.String()
	}
	b.WriteString(m.controlView(f, ""))
	return b.String()
}

// subView renders one FieldGroup member: full control when focused, else a
// `Title: answer` line.
func (m FormModel) subView(sub *Field, idx int, focused bool) string {
	num := fmt.Sprintf("%d) ", idx+1)
	if !focused {
		return "\n    " + FaintTxt.Render(num) + Text.Render(sub.Title+": ") + DimText.Render(m.summaryValue(sub))
	}
	var b strings.Builder
	label := Title.Render(num) + Text.Render(sub.Title+": ")
	if sub.Kind != FieldInput {
		b.WriteString("\n")
		b.WriteString(row("    "+Title.Render(num)+Text.Bold(true).Render(sub.Title), "", m.bodyW()))
		b.WriteString(m.descriptionBlock(sub, 6))
		label = ""
	}
	b.WriteString(m.controlView(sub, label))
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
		b.WriteString("\n")
		b.WriteString(optionRow("yes", on, on))
		b.WriteString("\n")
		b.WriteString(optionRow("no", !on, !on))
	case FieldMultiSelect:
		for i, opt := range f.Options {
			b.WriteString("\n")
			b.WriteString(optionRow(opt, f.Selected[opt], i == m.multiCursor))
		}
	case FieldSelect:
		for i, opt := range f.Options {
			num := fmt.Sprintf("%d) ", i+1)
			b.WriteString("\n")
			if i == m.multiCursor {
				b.WriteString("    " + Title.Render("❯ "+num) + Text.Bold(true).Render(opt))
				continue
			}
			b.WriteString("      " + DimText.Render(num) + Text.Render(opt))
		}
	}
	return b.String()
}

// optionRow renders one option as `marker label`. Options are picked with
// ↑/↓ and space/y/n; on-screen numbers are step ordinals.
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
			line = "      " + Selected.Render(s) + FaintTxt.Render("  ⏎ pick")
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
		hints = []string{KeyHint(fmt.Sprintf("1-%d", len(field.Options)), "choose")}
	case FieldMultiSelect:
		hints = []string{KeyHint("space", "on/off")}
	case FieldConfirm:
		hints = []string{KeyHint("y/n", "answer")}
	default:
		if len(m.sugg) > 0 {
			hints = []string{KeyHint("tab", "complete")}
		}
	}
	hints = append(hints, KeyHint("enter", "next"))
	if m.Current > 0 || m.subIdx > 0 {
		hints = append(hints, KeyHint("ctrl+b", "back"))
	}
	esc := KeyHint("esc", "done")
	if m.EscSaves {
		esc = KeyHint("esc", "save & back")
	}
	hints = append(hints, esc, MoreKeys())
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
		"Keep what you've entered so far, or leave without saving it.", &choice)
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
			return m.finish()
		}
	}
	return m.finish()
}

func (m FormModel) movePrev() (tea.Model, tea.Cmd) {
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
		case "y", "enter":
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
	title := Title.Render(m.Title)
	if m.Description != "" {
		title += "\n\n" + DimText.Render(m.Description)
	}

	yes, no := m.yesLabel(), m.noLabel()
	var buttons string
	if m.Value != nil && *m.Value {
		buttons = OK.Render(yes) + "  " + DimText.Render(no)
	} else {
		buttons = DimText.Render(yes) + "  " + Bad.Render(no)
	}

	content := title + "\n\n" + buttons
	body := lipgloss.NewStyle().Padding(1).Render(content)
	keys := m.Keys
	if keys == "" {
		keys = fmt.Sprintf("%s / %s", KeyHint("y", yes), KeyHint("n", no))
	}
	return Screen(body, Footer(keys, m.w), m.h)
}
