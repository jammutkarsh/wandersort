package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jammutkarsh/wandersort/pkg/path"
)

// StartScanMsg asks the shell to scan Paths. Force re-reads every file
// already added.
type StartScanMsg struct {
	Paths []string
	Force bool
}

// OpenReviewMsg asks the shell to review the proposal already in the database.
type OpenReviewMsg struct{}

// HomeErrMsg is a shell-side failure for the home screen to show above its
// footer. Never fatal.
type HomeErrMsg struct{ Err error }

// HomeNoteMsg is a shell-side success for the home screen to confirm above its
// footer, such as a settings save.
type HomeNoteMsg struct{ Text string }

// HomeConfig wires the home screen to the shell.
type HomeConfig struct {
	// Suggest completes a typed path; nil disables the dropdown.
	Suggest func(typed string) []string
	// LastScan is the finished scan's summary, rendered dim above the input —
	// empty on the first run, filled once a scan in this session has completed.
	LastScan []string
}

// maxHomeSuggestions caps the completion list under the input; it renders above
// the footer, so an unbounded list would push the folder list off screen.
const maxHomeSuggestions = 5

// HomeModel is the landing screen: a folder list built one path per enter (so
// spaces need no quoting), with directory completion.
type HomeModel struct {
	cfg   HomeConfig
	ti    textinput.Model
	paths *path.Resolver
	// added holds expanded paths, rendered home-relative
	added      []string
	sugg       []string
	suggCursor int // ↑/↓-picked completion; -1 = none picked
	err        error
	note       string // a success line; cleared by an error
	w, h       int

	// confirmForce is the full-screen ask before a force re-scan
	confirmForce bool
	showKeys     bool // the ? overlay is up
}

// homeKeys is the Add tab's full key list, behind ?.
var homeKeys = []KeyGroup{
	{"Folders", []KeyLine{
		{"enter", "add the folder typed"},
		{"tab", "complete the folder name"},
		{"↑", "take the last folder back to edit"},
		{"ctrl+x", "remove the last folder"},
	}},
	{"Planning", []KeyLine{
		{"enter", "on an empty line: plan the folders listed"},
		{"ctrl+g", "plan again, re-reading every file"},
	}},
	{"App", []KeyLine{
		{"shift+tab", "next tab"},
		{"ctrl+c", "quit"},
	}},
}

func NewHomeModel(cfg HomeConfig) HomeModel {
	paths := path.New()
	ti := textinput.New()
	ti.Prompt = lipgloss.NewStyle().Foreground(Primary).Render("❯ ")
	// Home-relative and OS-separated, not a hardcoded unix path: Windows'
	// equivalent lives under its own user profile drive, never "~/Pictures".
	ti.Placeholder = paths.RelativeToHome(filepath.Join(paths.HomeDir, "Pictures"))
	ti.Focus()
	m := HomeModel{cfg: cfg, ti: ti, paths: paths, suggCursor: -1}
	m.refresh()
	return m
}

// Busy is never true for the folder input: it is where a session waits.
func (m HomeModel) Busy() bool { return false }

func (m HomeModel) Init() tea.Cmd { return textinput.Blink }

func (m HomeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case HomeErrMsg:
		m.err, m.note = msg.Err, ""
		return m, nil
	case HomeNoteMsg:
		m.note, m.err = msg.Text, nil
		return m, nil
	case tea.KeyMsg:
		if m.confirmForce {
			// Only esc/enter here — no y/n, no arrows: a decision this
			// consequential (re-reading every file) gets exactly two keys.
			switch msg.String() {
			case "ctrl+c":
				return m, Left(Leave{Quit: true})
			case "enter":
				m.confirmForce = false
				paths := slices.Clone(m.added)
				return m, func() tea.Msg { return StartScanMsg{Paths: paths, Force: true} }
			case "esc":
				m.confirmForce = false
			}
			return m, nil
		}
		if m.showKeys {
			m.showKeys = false
			return m, nil
		}
		// Every letter is ordinary input here — the input is always focused —
		// so the screen's own commands are ctrl-chorded, and ? is only help
		// on an empty line.
		switch msg.String() {
		case "?":
			if m.ti.Value() == "" {
				m.showKeys = true
				return m, nil
			}
		case "ctrl+c":
			return m, Left(Leave{Quit: true})
		case "ctrl+g":
			if len(m.added) > 0 {
				m.confirmForce = true
			}
			return m, nil
		case "ctrl+x":
			m.drop(len(m.added) - 1)
			return m, nil
		case "up":
			switch {
			case m.suggCursor > -1: // walking the completion list
				m.suggCursor--
			case len(m.sugg) == 0 && len(m.added) > 0:
				// only once completions are closed (↑ is theirs first)
				return m, m.editLast()
			}
			return m, nil
		case "down":
			if m.suggCursor < len(m.sugg)-1 {
				m.suggCursor++
			}
			return m, nil
		case "tab":
			if len(m.sugg) > 0 {
				m.fill(max(m.suggCursor, 0))
			}
			return m, nil
		case "enter":
			// an arrowed-onto completion: pick it instead of adding the folder
			if m.suggCursor >= 0 && m.suggCursor < len(m.sugg) {
				m.fill(m.suggCursor)
				return m, nil
			}
			return m.enter()
		}
	}

	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(msg)
	if _, isKey := msg.(tea.KeyMsg); isKey {
		m.refresh()
	}
	return m, cmd
}

// editLast pulls the most recently added folder back into the input.
func (m *HomeModel) editLast() tea.Cmd {
	i := len(m.added) - 1
	m.ti.SetValue(m.paths.RelativeToHome(m.added[i]))
	m.ti.CursorEnd()
	m.drop(i)
	m.refresh()
	return m.ti.Focus()
}

// drop removes one folder.
func (m *HomeModel) drop(i int) {
	if i < 0 || i >= len(m.added) {
		return
	}
	m.added = slices.Delete(m.added, i, i+1)
}

// enter adds the typed folder to the list, or starts the scan when the input
// is empty.
func (m HomeModel) enter() (tea.Model, tea.Cmd) {
	typed := strings.TrimSpace(m.ti.Value())
	if typed == "" {
		if len(m.added) == 0 {
			m.err = fmt.Errorf("type a folder to scan first")
			return m, nil
		}
		paths := slices.Clone(m.added)
		return m, func() tea.Msg { return StartScanMsg{Paths: paths} }
	}

	dir := m.paths.ExpandPath(typed)
	st, err := os.Stat(dir)
	switch {
	case err != nil:
		m.err = fmt.Errorf("%s — no such folder", typed)
		return m, nil
	case !st.IsDir():
		m.err = fmt.Errorf("%s is not a folder", typed)
		return m, nil
	}
	m.err = nil
	if !slices.Contains(m.added, dir) {
		m.added = append(m.added, dir)
	}
	m.ti.SetValue("")
	m.refresh()
	return m, nil
}

// fill writes the picked completion into the input with a trailing "/" (every
// suggestion is a directory) and refreshes the list below it.
func (m *HomeModel) fill(i int) {
	m.ti.SetValue(m.sugg[i] + "/")
	m.ti.CursorEnd()
	m.refresh()
}

func (m *HomeModel) refresh() {
	m.suggCursor = -1
	m.sugg = nil
	if m.cfg.Suggest != nil {
		m.sugg = m.cfg.Suggest(m.ti.Value())
	}
}

func (m HomeModel) View() string {
	if m.confirmForce {
		// built per frame; this screen's own keys drive it
		yes := true
		cm := NewConfirmModel("Force re-scan?",
			"Re-reads every already-scanned file from disk instead of skipping "+
				"unchanged ones. Slower — use after upgrading WanderSort, or if "+
				"a file's metadata looks wrong.", &yes)
		cm.Keys = fmt.Sprintf("%s / %s", KeyHint("enter", "confirm"), KeyHint("esc", "cancel"))
		cm.w, cm.h = m.w, m.h
		return cm.View()
	}

	var b strings.Builder
	b.WriteString("\n")
	for _, l := range m.cfg.LastScan {
		b.WriteString(row("  "+OK.Render("✓ ")+DimText.Render(l), "", m.w))
		b.WriteString("\n")
	}
	if len(m.cfg.LastScan) > 0 {
		b.WriteString("\n")
	}

	title := "Add photos from"
	if len(m.cfg.LastScan) > 0 {
		title = "Add more photos from"
	}
	b.WriteString(row("  "+Text.Bold(true).Render(title), "", m.w))
	b.WriteString("\n\n")
	for _, p := range m.added {
		b.WriteString(row("    "+OK.Render("✓ ")+Text.Render(m.paths.RelativeToHome(p)), "", m.w))
		b.WriteString("\n")
	}
	b.WriteString("  ")
	b.WriteString(m.ti.View())
	b.WriteString("\n")

	start, end := suggWindow(len(m.sugg), m.suggCursor, maxHomeSuggestions)
	if start > 0 {
		b.WriteString(row("      "+FaintTxt.Render(fmt.Sprintf("↑ %d more", start)), "", m.w) + "\n")
	}
	for i := start; i < end; i++ {
		var line string
		switch {
		case i == m.suggCursor:
			line = "      " + Selected.Render(m.sugg[i]) + FaintTxt.Render("  ⏎ pick")
		case i == 0 && m.suggCursor < 0:
			line = "      " + FaintTxt.Render("· ") + DimText.Render(m.sugg[i]) + FaintTxt.Render("  ⇥ tab")
		default:
			line = "      " + FaintTxt.Render("· ") + DimText.Render(m.sugg[i])
		}
		b.WriteString(row(line, "", m.w) + "\n")
	}
	if rest := len(m.sugg) - end; rest > 0 {
		b.WriteString(row("      "+FaintTxt.Render(fmt.Sprintf("↓ %d more", rest)), "", m.w) + "\n")
	}

	view := Screen(b.String(), m.footer(), m.h)
	if m.showKeys {
		return KeyHelp(view, homeKeys, m.w, m.h)
	}
	return view
}

func (m HomeModel) footer() string {
	var b strings.Builder
	if m.note != "" {
		b.WriteString(row(OK.Render("✓ ")+DimText.Render(m.note), "", m.w))
		b.WriteString("\n")
	}
	if m.err != nil {
		b.WriteString(row(Attn.Render("⚠ "+m.err.Error()), "", m.w))
		b.WriteString("\n")
	}
	if len(m.added) > 0 && len(m.sugg) == 0 {
		b.WriteString(row("  "+FaintTxt.Render("Enter on an empty line plans these folders."), "", m.w))
		b.WriteString("\n")
	}
	var hints []string
	if len(m.sugg) > 0 {
		hints = append(hints, KeyHint("tab", "complete"))
	}
	hints = append(hints, KeyHint("enter", "add"))
	if len(m.added) > 0 && len(m.sugg) == 0 {
		hints = append(hints, KeyHint("↑", "edit last"))
	}
	hints = append(hints, MoreKeys())
	b.WriteString(Footer(strings.Join(hints, "   "), m.w))
	return b.String()
}
