package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// SettingRow is one line of the settings list: what it is, its current value,
// and how to change it.
type SettingRow struct {
	Label, Value string
	// Edit builds the one-step form that changes this setting, over a fresh
	// copy of the settings, and the save that writes them. nil: shown, fixed.
	Edit func() ([]*Field, func() error)
}

// SettingsSavedMsg tells the shell a setting was saved.
type SettingsSavedMsg struct{}

type settingsEditDoneMsg struct{ leave Leave }

// SettingsModel lists a library's settings; enter changes one, and the list
// comes back with the new value.
type SettingsModel struct {
	rows     func() []SettingRow
	list     []SettingRow
	note     string
	cursor   int
	edit     *FormModel
	err      error
	saved    bool
	w, h     int
	showKeys bool
}

var settingsKeys = []KeyGroup{
	{"Settings", []KeyLine{
		{"↑ ↓", "move"},
		{"enter", "change the setting"},
		{"esc", "back"},
		{"ctrl+c", "quit"},
	}},
}

// NewSettingsModel lists rows(), re-read after every save; note sits under the
// list.
func NewSettingsModel(rows func() []SettingRow, note string) SettingsModel {
	m := SettingsModel{rows: rows, note: note, list: rows()}
	m.cursor = m.firstEditable()
	return m
}

func (m SettingsModel) firstEditable() int {
	for i, r := range m.list {
		if r.Edit != nil {
			return i
		}
	}
	return 0
}

func (m SettingsModel) Busy() bool { return false }

func (m SettingsModel) Init() tea.Cmd { return nil }

func (m SettingsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case settingsEditDoneMsg:
		m.edit = nil
		switch {
		case msg.leave.Quit:
			return m, Left(Leave{Quit: true})
		case msg.leave.Err != nil:
			m.err = msg.leave.Err
		case !msg.leave.Aborted:
			m.err, m.saved = nil, true
			m.list = m.rows()
			return m, func() tea.Msg { return SettingsSavedMsg{} }
		}
		return m, nil
	}
	if m.edit != nil {
		next, cmd := m.edit.Update(msg)
		f := next.(FormModel)
		m.edit = &f
		return m, cmd
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		return m.handleKey(k)
	}
	return m, nil
}

func (m SettingsModel) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.showKeys {
		m.showKeys = false
		return m, nil
	}
	m.saved = false
	switch k.String() {
	case "?":
		m.showKeys = true
	case "ctrl+c":
		return m, Left(Leave{Quit: true})
	case "esc":
		return m, Left(Leave{Aborted: true}) // every change was saved as it was made
	case "up":
		for i := m.cursor - 1; i >= 0; i-- {
			if m.list[i].Edit != nil {
				m.cursor = i
				break
			}
		}
	case "down":
		for i := m.cursor + 1; i < len(m.list); i++ {
			if m.list[i].Edit != nil {
				m.cursor = i
				break
			}
		}
	case "enter":
		r := m.list[m.cursor]
		if r.Edit == nil {
			return m, nil
		}
		fields, save := r.Edit()
		f := NewFormModel(fields, save)
		f.Heading = "Settings › " + r.Label
		f.Then = func(l Leave) tea.Cmd { return func() tea.Msg { return settingsEditDoneMsg{l} } }
		sized, _ := f.Update(tea.WindowSizeMsg{Width: m.w, Height: m.h})
		f = sized.(FormModel)
		m.edit = &f
		return m, f.Init()
	}
	return m, nil
}

func (m SettingsModel) View() string {
	if m.edit != nil {
		return m.edit.View()
	}
	labelW := 0
	for _, r := range m.list {
		labelW = max(labelW, len(r.Label))
	}
	var b strings.Builder
	b.WriteString("\n" + row("  "+Text.Bold(true).Render("Settings for this library"), "", m.w) + "\n\n")
	for i, r := range m.list {
		label := r.Label + strings.Repeat(" ", labelW-len(r.Label)+3)
		right, mark := FaintTxt.Render("›"), "  "
		if r.Edit == nil {
			right = FaintTxt.Render("can't change")
		}
		if i == m.cursor {
			mark = Title.Render("❯ ")
			right = Title.Render("›")
		}
		line := row("  "+mark+DimText.Render(label)+Text.Render(r.Value), right+"  ", m.w)
		if i == m.cursor {
			line = Selected.Render(row("  ❯ "+label+r.Value, "›  ", m.w))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	switch {
	case m.err != nil:
		b.WriteString(row("  "+Bad.Render("✗ ")+Text.Render(m.err.Error()), "", m.w) + "\n")
	case m.saved:
		b.WriteString(row("  "+OK.Render("✓ ")+Text.Render("Saved. Files not yet copied are planned again."), "", m.w) + "\n")
	case m.note != "":
		b.WriteString(row("  "+FaintTxt.Render(m.note), "", m.w) + "\n")
	}
	footer := Footer(KeyHint("↑↓", "move")+"   "+KeyHint("enter", "change")+"   "+KeyHint("esc", "back")+"   "+MoreKeys(), m.w)
	view := Screen(b.String(), footer, m.h)
	if m.showKeys {
		return KeyHelp(view, settingsKeys, m.w, m.h)
	}
	return view
}
