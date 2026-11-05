package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/human"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

// StartScanMsg asks the shell to scan Paths. Force re-reads every file
// already added.
type StartScanMsg struct {
	Paths []string
	Force bool
}

// DiscardPlanMsg asks the shell to take every file not yet copied off the list, so the
// folders can be added again from scratch.
type DiscardPlanMsg struct{}

// DiscardPlanAsk is the question before a DiscardPlanMsg.
func DiscardPlanAsk(planned int) Ask {
	return Ask{
		Title: fmt.Sprintf("Start over with %s?", human.Plural(planned, "file", "files")),
		Detail: "Takes the files not copied yet off the list, along with your edits from Organise. Copied files stay. " +
			"Add the folders again to sort them again.",
		Action: "start over",
		Msg:    DiscardPlanMsg{},
	}
}

// ResetAsk is the question before `admin db --reset`. Placed files stay on
// disk but untracked, so re-importing a card copies them again: the question
// says so.
func ResetAsk(placed int) Ask {
	a := Ask{
		Title:  "Reset the library?",
		Detail: "Forgets everything scanned, planned and edited. Settings stay. Undo with admin db --restore until the next copy or reset.",
	}
	if placed > 0 {
		a.Title = fmt.Sprintf("Forget the %s already in the library?", human.Plural(placed, "file", "files"))
		a.Detail = "They stay on disk, but adding them again copies them twice, and check skips them. " + a.Detail
	}
	return a
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
	// Check refuses a folder as it is added (one inside the library, say);
	// nil accepts every folder.
	Check func(dir string) error
	// LastScan is the finished scan's summary, rendered dim above the input —
	// empty on the first run, filled once a scan in this session has completed.
	LastScan []string
	// History is the folders scanned before, newest first; ↑ on an empty line
	// walks it, after the folders added here.
	History []string
	// PickFolder opens the system's folder dialog and returns the folder
	// chosen ("" when closed without one); nil leaves the dialog out.
	PickFolder func() (string, error)
}

// folderPickedMsg is the folder dialog's answer.
type folderPickedMsg struct {
	dir string
	err error
}

// pickFolder opens the dialog off the UI goroutine.
func (m HomeModel) pickFolder() tea.Cmd {
	pick := m.cfg.PickFolder
	return func() tea.Msg {
		dir, err := pick()
		return folderPickedMsg{dir: dir, err: err}
	}
}

// HomeLibraryMsg is where the open library's files stand, for the status above
// the input: Planned wait to be copied (ctrl+r discards them), Placed are in
// the library, Edits wait in Organise's draft.
type HomeLibraryMsg struct{ Planned, Placed, Edits int }

// messageFor is how long a warning or note stays above the footer.
const messageFor = 5 * time.Second

// messageExpiredMsg ends the message numbered shown, if it is still up.
type messageExpiredMsg struct{ shown int }

// maxHomeSuggestions caps the completion list under the input; it renders above
// the footer, so an unbounded list would push the folder list off screen.
const maxHomeSuggestions = 5

// HomeModel is the landing screen: a folder list built by dropping folders on
// the terminal or typing one path per enter (so spaces need no quoting), with
// directory completion.
type HomeModel struct {
	cfg   HomeConfig
	err   error
	paths *path.Resolver

	ask  *Ask   // a full-screen question before something hard to undo
	note string // a success line; cleared by an error
	// added holds expanded paths, rendered home-relative
	added []string
	sugg  []string
	// ↑/↓ walk: the folders added here (newest first), then History. The
	// added one on screen is lifted out of the list while shown, so editing
	// it replaces it.
	walk       []string
	ti         textinput.Model
	lib        HomeLibraryMsg
	suggCursor int // ↑/↓-picked completion; -1 = none picked
	histIdx    int // walk entry in the input; -1 = not walking
	lifted     int // where the shown entry sat in added; -1 = it wasn't there
	shown      int // which err/note is up, so only its own timer clears it
	w, h       int

	showKeys bool // the ? overlay is up
	picking  bool // the folder window is open; one at a time
}

// homeKeys is the Add tab's full key list, behind ?.
var homeKeys = []KeyGroup{
	{"Folders", []KeyLine{
		{"drop", "drag a folder onto the terminal to add it"},
		{"ctrl+o", "choose a folder in a window"},
		{"enter", "add the folder typed"},
		{"tab", "complete the folder name"},
		{"↑ ↓", "on an empty line: recent folders"},
		{"ctrl+x", "remove the last folder"},
	}},
	{"Adding", []KeyLine{
		{"enter", "on an empty line: scan the folders listed"},
		{"ctrl+g", "add again, reading every file again (slower)"},
		{"ctrl+r", "start over: take the files not copied yet off the list"},
	}},
	{"App", []KeyLine{
		{"shift+tab", "next tab"},
		{"esc esc", "quit"},
		{"ctrl+c", "quit"},
	}},
}

func NewHomeModel(cfg HomeConfig) HomeModel {
	paths := path.New()
	ti := textinput.New()
	ti.Prompt = Prompt()
	ti.Placeholder = "Drop a folder here, or type its path"
	ti.Focus()
	m := HomeModel{cfg: cfg, ti: ti, paths: paths, suggCursor: -1, histIdx: -1, lifted: -1}
	m.refresh()
	return m
}

// Busy is never true for the folder input: it is where a session waits.
func (m HomeModel) Busy() bool { return false }

// Nested reports a question or the key list up.
func (m HomeModel) Nested() bool { return m.ask != nil || m.showKeys }

func (m HomeModel) Init() tea.Cmd { return textinput.Blink }

func (m HomeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case HomeErrMsg:
		return m, m.show(msg.Err, "")
	case HomeNoteMsg:
		return m, m.show(nil, msg.Text)
	case messageExpiredMsg:
		if msg.shown == m.shown {
			m.err, m.note = nil, ""
		}
		return m, nil
	case HomeLibraryMsg:
		m.lib = msg
		return m, nil
	case folderPickedMsg:
		m.picking = false
		switch {
		case msg.err != nil:
			return m, m.show(msg.err, "")
		case msg.dir == "":
			return m, nil
		}
		cmd, _ := m.addDropped(msg.dir)
		return m, cmd
	case tea.KeyMsg:
		if m.ask != nil {
			done, cmd := m.ask.Key(msg)
			if done {
				m.ask = nil
			}
			return m, cmd
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
		case "ctrl+o":
			if m.cfg.PickFolder == nil || m.picking {
				return m, nil
			}
			m.picking = true
			return m, m.pickFolder()
		case "ctrl+g":
			if len(m.added) > 0 {
				// a decision this consequential (re-reading every file) gets
				// exactly two keys: enter or esc
				m.ask = &Ask{
					Title:  "Read every file again?",
					Detail: "Slower. Use it if dates or places look wrong.",
					Action: "read again",
					Msg:    StartScanMsg{Paths: slices.Clone(m.added), Force: true},
				}
			}
			return m, nil
		case "ctrl+r":
			if m.lib.Planned > 0 {
				a := DiscardPlanAsk(m.lib.Planned)
				m.ask = &a
			}
			return m, nil
		case "ctrl+x":
			m.drop(len(m.added) - 1)
			return m, nil
		case "up":
			switch {
			case m.suggCursor > -1: // walking the completion list
				m.suggCursor--
			case m.histIdx >= 0 || m.ti.Value() == "":
				m.walkHistory(1)
			}
			return m, nil
		case "down":
			switch {
			case m.histIdx >= 0:
				m.walkHistory(-1)
			case m.suggCursor < len(m.sugg)-1:
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
		// a drop arrives as a paste: onto an empty line it adds the folders at
		// once, anywhere else it is text for the line
		if msg.Paste && m.ti.Value() == "" {
			if cmd, added := m.addDropped(string(msg.Runes)); added {
				return m, cmd
			}
		}
	}

	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(msg)
	if _, isKey := msg.(tea.KeyMsg); isKey {
		m.refresh()
	}
	return m, cmd
}

// walkHistory steps like a shell history, 1 older and -1 newer: first the
// folders added here, then the ones scanned before. Past the newest the line
// empties. Enter adds the one shown; an added one shown is out of the list
// until the walk moves on, so it can be edited.
func (m *HomeModel) walkHistory(step int) {
	if m.histIdx < 0 {
		m.walk = slices.Clone(m.added)
		slices.Reverse(m.walk)
		for _, d := range m.cfg.History {
			if !slices.Contains(m.walk, d) {
				m.walk = append(m.walk, d)
			}
		}
	}
	if m.lifted >= 0 {
		// ctrl+x may have shortened the list since the folder was lifted
		m.added = slices.Insert(m.added, min(m.lifted, len(m.added)), m.walk[m.histIdx])
		m.lifted = -1
	}
	i := min(m.histIdx+step, len(m.walk)-1)
	if i < 0 {
		m.ti.SetValue("")
		m.refresh()
		return
	}
	if j := slices.Index(m.added, m.walk[i]); j >= 0 {
		m.added = slices.Delete(m.added, j, j+1)
		m.lifted = j
	}
	m.ti.SetValue(m.paths.RelativeToHome(m.walk[i]))
	m.ti.CursorEnd()
	// no completions while walking: ↑/↓ stay the walk's
	m.sugg, m.suggCursor, m.histIdx = nil, -1, i
}

// Add lists folders as if typed, for a run to fix and plan again.
func (m *HomeModel) Add(dirs ...string) {
	for _, d := range dirs {
		if !slices.Contains(m.added, d) {
			m.added = append(m.added, d)
		}
	}
}

// drop removes one folder.
func (m *HomeModel) drop(i int) {
	if i < 0 || i >= len(m.added) {
		return
	}
	m.added = slices.Delete(m.added, i, i+1)
}

// enter adds the typed folder to the list, or starts the scan when the input
// is empty and a folder is listed.
func (m HomeModel) enter() (tea.Model, tea.Cmd) {
	if strings.TrimSpace(m.ti.Value()) == "" {
		if len(m.added) == 0 {
			return m, m.show(fmt.Errorf("Add a folder"), "")
		}
		paths := slices.Clone(m.added)
		return m, func() tea.Msg { return StartScanMsg{Paths: paths} }
	}
	cmd, _ := m.addDropped(m.ti.Value())
	return m, cmd
}

// addDropped lists every folder in text (typed, pasted or dropped) and clears
// the input. One that can't be added stops it, stays in the input and is shown
// as the warning; added reports that text held a folder.
func (m *HomeModel) addDropped(text string) (cmd tea.Cmd, added bool) {
	dirs := m.paths.Dropped(text)
	for _, dir := range dirs {
		shown := m.paths.RelativeToHome(dir)
		st, err := os.Stat(dir)
		switch {
		case err != nil:
			return m.show(fmt.Errorf("%s: no such folder", shown), ""), false
		case !st.IsDir():
			return m.show(fmt.Errorf("%s is not a folder", shown), ""), false
		}
		// refused here, while it is still in the input to change
		if m.cfg.Check != nil {
			if err := m.cfg.Check(dir); err != nil {
				return m.show(err, ""), false
			}
		}
		m.err = nil
		if !slices.Contains(m.added, dir) {
			m.added = append(m.added, dir)
		}
		m.ti.SetValue("")
		m.refresh()
	}
	return nil, len(dirs) > 0
}

// show puts up a warning or a note (one replaces the other) for messageFor.
func (m *HomeModel) show(err error, note string) tea.Cmd {
	m.err, m.note = err, note
	m.shown++
	shown := m.shown
	return tea.Tick(messageFor, func(time.Time) tea.Msg { return messageExpiredMsg{shown} })
}

// fill writes the picked completion into the input with a trailing "/" (every
// suggestion is a directory) and refreshes the list below it.
func (m *HomeModel) fill(i int) {
	m.ti.SetValue(m.sugg[i] + "/")
	m.ti.CursorEnd()
	m.refresh()
}

func (m *HomeModel) refresh() {
	// typing ends a walk; a lifted folder stays out, being edited
	m.histIdx, m.walk, m.lifted = -1, nil, -1
	m.suggCursor = -1
	m.sugg = nil
	if m.cfg.Suggest != nil {
		m.sugg = m.cfg.Suggest(m.ti.Value())
	}
}

func (m HomeModel) View() string {
	if m.ask != nil {
		return m.ask.View(m.w, m.h)
	}

	var b strings.Builder
	b.WriteString("\n")
	for _, l := range m.cfg.LastScan {
		b.WriteString(row(OK.Render("✓ ")+DimText.Render(l), "", m.w))
		b.WriteString("\n")
	}
	if len(m.cfg.LastScan) > 0 {
		b.WriteString("\n")
	}
	if lines := m.libraryLines(); len(lines) > 0 {
		for _, l := range lines {
			b.WriteString(row(l, "", m.w) + "\n")
		}
		b.WriteString("\n")
	}

	title := "Add photos from"
	if len(m.cfg.LastScan) > 0 {
		title = "Add more photos from"
	}
	b.WriteString(row(Text.Bold(true).Render(title), "", m.w))
	b.WriteString("\n\n")
	for _, p := range m.added {
		b.WriteString(row("  "+OK.Render("✓ ")+Text.Render(m.paths.RelativeToHome(p)), "", m.w))
		b.WriteString("\n")
	}
	b.WriteString(m.ti.View())
	b.WriteString("\n")

	start, end := suggWindow(len(m.sugg), m.suggCursor, maxHomeSuggestions)
	if start > 0 {
		b.WriteString(row("    "+FaintTxt.Render(fmt.Sprintf("↑ %d more", start)), "", m.w) + "\n")
	}
	for i := start; i < end; i++ {
		var line string
		switch {
		case i == m.suggCursor:
			line = "   " + Pill(m.sugg[i]) + FaintTxt.Render("  ⏎ pick")
		case i == 0 && m.suggCursor < 0:
			line = "    " + FaintTxt.Render("· ") + DimText.Render(m.sugg[i]) + FaintTxt.Render("  ⇥ tab")
		default:
			line = "    " + FaintTxt.Render("· ") + DimText.Render(m.sugg[i])
		}
		b.WriteString(row(Clickable(line, append(MoveKeys(m.suggCursor, i), "enter")...), "", m.w) + "\n")
	}
	if rest := len(m.sugg) - end; rest > 0 {
		b.WriteString(row("    "+FaintTxt.Render(fmt.Sprintf("↓ %d more", rest)), "", m.w) + "\n")
	}

	view := Screen(b.String(), m.footer(), m.h)
	if m.showKeys {
		return KeyHelp(view, homeKeys, m.w, m.h)
	}
	return view
}

// libraryLines is where the library's files stand: planned (and edited in
// Organise) but not copied, and copied in. Empty for an empty library.
func (m HomeModel) libraryLines() []string {
	if m.lib.Planned == 0 && m.lib.Placed == 0 {
		return nil
	}
	planned := "nothing"
	if m.lib.Planned > 0 {
		planned = human.Plural(m.lib.Planned, "file", "files")
		if m.lib.Edits > 0 {
			planned += ", " + human.Plural(m.lib.Edits, "edit", "edits") + " from Organise"
		}
	}
	return []string{
		DimText.Render("To copy          ") + Text.Render(planned),
		DimText.Render("In the library   ") + Text.Render(human.Plural(m.lib.Placed, "file", "files")),
	}
}

func (m HomeModel) footer() string {
	var b strings.Builder
	if m.note != "" {
		b.WriteString(row(OK.Render("✓ ")+DimText.Render(m.note), "", m.w))
		b.WriteString("\n")
	}
	if m.err != nil {
		b.WriteString(row(Attn.Render("⚠ "+Explain(m.err)), "", m.w))
		b.WriteString("\n")
	}
	var hints []string
	if len(m.sugg) > 0 {
		hints = append(hints, KeyHint("tab", "complete"))
	}
	// enter scans once folders are listed and the line is empty
	if len(m.added) > 0 && m.ti.Value() == "" {
		hints = append(hints, KeyHint("enter", "scan"))
	} else {
		hints = append(hints, KeyHint("enter", "add"))
	}
	if m.cfg.PickFolder != nil && !m.picking {
		hints = append(hints, KeyHint("ctrl+o", "choose a folder"))
	}
	if len(m.added)+len(m.cfg.History) > 0 && (m.histIdx >= 0 || m.ti.Value() == "") {
		hints = append(hints, KeyHint("↑↓", "recent folders"))
	}
	hints = append(hints, MoreKeys())
	b.WriteString(Footer(strings.Join(hints, "   "), m.w))
	return b.String()
}
