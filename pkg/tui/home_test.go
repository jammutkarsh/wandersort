package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// typeHome feeds text then enter, the way a user adds one folder.
func typeHome(m HomeModel, text string) HomeModel {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
	m = next.(HomeModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(HomeModel)
}

// viewText is a view as text, hint spaces (non-breaking) included.
func viewText(view string) string {
	return strings.ReplaceAll(ansi.Strip(view), "\u00a0", " ")
}

func TestHome(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// One folder per enter is the whole multi-path story: a list built by
		// enter, never a comma-separated string the user has to escape.
		{"AddsOneFolderPerEnterAndDedupes", func(t *testing.T) {
			dir := t.TempDir()
			m := NewHomeModel(HomeConfig{})

			m = typeHome(m, dir)
			if got := m.added; len(got) != 1 || got[0] != dir {
				t.Fatalf("added = %v, want [%s]", got, dir)
			}
			m = typeHome(m, dir) // same folder again
			if got := m.added; len(got) != 1 {
				t.Errorf("adding the same folder twice = %v, want it deduped", got)
			}
			if v := ansi.Strip(m.View()); !strings.Contains(v, dir) {
				t.Errorf("view should list the added folder:\n%s", v)
			}
		}},
		// Folders under $HOME render as ~/… — the way they were typed and the
		// way every completion offers them. The scan still gets the expanded
		// path.
		{"ShowsHomeFoldersRelativeToHome", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			sub := filepath.Join(home, "Raw_JPG")
			if err := os.Mkdir(sub, 0o755); err != nil {
				t.Fatal(err)
			}

			m := typeHome(NewHomeModel(HomeConfig{}), "~/Raw_JPG")
			if got := m.added; len(got) != 1 || got[0] != sub {
				t.Fatalf("added = %v, want the expanded [%s]", got, sub)
			}
			v := ansi.Strip(m.View())
			if !strings.Contains(v, filepath.Join("~", "Raw_JPG")) || strings.Contains(v, sub) {
				t.Errorf("want the folder listed as ~/Raw_JPG, not spelled out:\n%s", v)
			}
		}},
		// A path that isn't a folder is a typo, not a scan: it reports and
		// leaves the list alone.
		{"RejectsAPathThatIsNotADirectory", func(t *testing.T) {
			m := NewHomeModel(HomeConfig{})
			m = typeHome(m, "/definitely/not/here")
			if len(m.added) != 0 {
				t.Fatalf("added = %v, want none added", m.added)
			}
			if !strings.Contains(ansi.Strip(m.View()), "no such folder") {
				t.Errorf("a missing folder should say so:\n%s", ansi.Strip(m.View()))
			}
		}},
		// Enter on an empty input is "start", and only once there's something
		// to scan.
		{"EmptyEnterStartsTheScan", func(t *testing.T) {
			m := NewHomeModel(HomeConfig{})
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if !strings.Contains(ansi.Strip(next.(HomeModel).View()), "Add a folder") {
				t.Fatal("enter with no folders should ask for one, not start a scan")
			}

			dir := t.TempDir()
			m = typeHome(m, dir)
			_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("enter on an empty input with folders queued should start the scan")
			}
			msg, ok := cmd().(StartScanMsg)
			if !ok {
				t.Fatalf("cmd produced %T, want StartScanMsg", cmd())
			}
			if len(msg.Paths) != 1 || msg.Paths[0] != dir {
				t.Errorf("StartScanMsg.Paths = %v, want [%s]", msg.Paths, dir)
			}
		}},
		// ↑/↓ on an empty line walk the folders scanned before, newest first,
		// like a shell; enter adds the one shown. ctrl+x drops the last folder.
		{"ArrowsWalkScannedFolders", func(t *testing.T) {
			older, newer := t.TempDir(), t.TempDir()
			m := NewHomeModel(HomeConfig{History: []string{newer, older}})
			press := func(k tea.KeyType) {
				next, _ := m.Update(tea.KeyMsg{Type: k})
				m = next.(HomeModel)
			}

			for _, step := range []struct {
				key  tea.KeyType
				want string
			}{
				{tea.KeyUp, newer},
				{tea.KeyUp, older},
				{tea.KeyUp, older}, // the oldest stays
				{tea.KeyDown, newer},
				{tea.KeyDown, ""}, // past the newest the line empties
				{tea.KeyUp, newer},
			} {
				press(step.key)
				if got := m.ti.Value(); got != step.want {
					t.Fatalf("after %v: input = %q, want %q", step.key, got, step.want)
				}
			}

			press(tea.KeyEnter)
			if got := m.added; len(got) != 1 || got[0] != newer {
				t.Fatalf("enter on a history entry: added = %v, want [%s]", got, newer)
			}
			press(tea.KeyCtrlX)
			if got := m.added; len(got) != 0 {
				t.Fatalf("ctrl+x should remove the folder, got %v", got)
			}
		}},
		// ↑ reaches the folders added here first, newest first, lifted out of
		// the list while shown so editing one replaces it; then the history.
		{"ArrowsReachAddedFoldersFirst", func(t *testing.T) {
			added, scanned := t.TempDir(), t.TempDir()
			m := typeHome(NewHomeModel(HomeConfig{History: []string{scanned, added}}), added)
			press := func(k tea.KeyType) {
				next, _ := m.Update(tea.KeyMsg{Type: k})
				m = next.(HomeModel)
			}

			press(tea.KeyUp)
			if m.ti.Value() != added || len(m.added) != 0 {
				t.Fatalf("↑: input %q, list %v; want the added folder lifted out", m.ti.Value(), m.added)
			}
			press(tea.KeyUp)
			if m.ti.Value() != scanned || len(m.added) != 1 || m.added[0] != added {
				t.Fatalf("↑↑: input %q, list %v; want history, the added folder back", m.ti.Value(), m.added)
			}
			press(tea.KeyUp) // the history's copy of the added folder isn't shown twice
			if m.ti.Value() != scanned {
				t.Errorf("↑↑↑: input %q, want the walk to stop at its end", m.ti.Value())
			}
			press(tea.KeyDown)
			press(tea.KeyDown)
			if m.ti.Value() != "" || len(m.added) != 1 {
				t.Errorf("↓ past the newest: input %q, list %v; want empty, list whole", m.ti.Value(), m.added)
			}
		}},
		// Dropping the folder lifted into the input for editing, then walking
		// on, must not index past the shortened list.
		{"WalkAfterDroppingTheLiftedFolder", func(t *testing.T) {
			m := typeHome(typeHome(NewHomeModel(HomeConfig{}), t.TempDir()), t.TempDir())
			press := func(k tea.KeyType) {
				next, _ := m.Update(tea.KeyMsg{Type: k})
				m = next.(HomeModel)
			}
			press(tea.KeyUp)    // the newest is lifted into the input
			press(tea.KeyCtrlX) // the other one is dropped, the list is empty
			press(tea.KeyUp)
			press(tea.KeyDown)
		}},
		// A refused folder stays in the input with the reason, not in the list.
		{"CheckRefusesBeforeAdding", func(t *testing.T) {
			dir := t.TempDir()
			m := typeHome(NewHomeModel(HomeConfig{Check: func(string) error { return errBadValue }}), dir)
			if len(m.added) != 0 || m.ti.Value() != dir || m.err == nil {
				t.Errorf("list %v, input %q, err %v; want nothing added, the folder still typed, the reason shown", m.added, m.ti.Value(), m.err)
			}
		}},
		// A drop reaches the terminal as a paste of quoted or escaped paths: on
		// an empty line every folder in it is listed at once, no enter needed.
		{"DroppedFoldersAreAddedAtOnce", func(t *testing.T) {
			parent := t.TempDir()
			spaced, plain := filepath.Join(parent, "My Photos"), filepath.Join(parent, "raw")
			for _, d := range []string{spaced, plain} {
				if err := os.Mkdir(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			m := NewHomeModel(HomeConfig{})
			drop := "'" + spaced + "' '" + plain + "' "
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(drop), Paste: true})
			m = next.(HomeModel)
			if got := m.added; len(got) != 2 || got[0] != spaced || got[1] != plain {
				t.Fatalf("added = %v, want [%s %s]", got, spaced, plain)
			}
			if m.ti.Value() != "" {
				t.Errorf("input = %q, want it emptied once the folders are listed", m.ti.Value())
			}
			if v := viewText(m.View()); !strings.Contains(v, "enter scan") {
				t.Errorf("a listed folder should offer the scan in the footer:\n%s", v)
			}
			m = typeHome(m, "~/typing")
			if v := viewText(m.View()); !strings.Contains(v, "enter add") {
				t.Errorf("a line being typed should offer enter add:\n%s", v)
			}
		}},
		// A paste that is no folder is text for the line, so it can be fixed.
		{"PastedTextThatIsNoFolderStaysInTheInput", func(t *testing.T) {
			m := NewHomeModel(HomeConfig{})
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/definitely/not/here"), Paste: true})
			m = next.(HomeModel)
			if len(m.added) != 0 || m.ti.Value() != "/definitely/not/here" {
				t.Errorf("list %v, input %q; want nothing added, the text kept", m.added, m.ti.Value())
			}
		}},
		// Completions come from the injected Suggest; tab fills the top one.
		{"TabFillsTheTopCompletion", func(t *testing.T) {
			m := NewHomeModel(HomeConfig{Suggest: func(string) []string { return []string{"~/Pictures", "~/Public"} }})
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("~/P")})
			m = next.(HomeModel)
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
			m = next.(HomeModel)
			if got := m.ti.Value(); got != "~/Pictures/" {
				t.Errorf("tab filled %q, want the top completion with a trailing slash", got)
			}
		}},
		// A lock held by another process reaches the screen as a message and
		// renders — it must never take the app down.
		{"ShowsShellErrorsWithoutQuitting", func(t *testing.T) {
			m := NewHomeModel(HomeConfig{})
			next, _ := m.Update(HomeErrMsg{Err: errAlreadyRunning})
			if !strings.Contains(ansi.Strip(next.(HomeModel).View()), "already running") {
				t.Errorf("the error should render:\n%s", ansi.Strip(next.(HomeModel).View()))
			}
		}},
		// A warning clears when its own timer ends, never when an older one
		// does after a newer message replaced it.
		{"MessagesExpire", func(t *testing.T) {
			m := NewHomeModel(HomeConfig{})
			next, _ := m.Update(HomeErrMsg{Err: errAlreadyRunning})
			next, _ = next.Update(HomeNoteMsg{Text: "saved"})
			next, _ = next.Update(messageExpiredMsg{shown: 1}) // the warning's timer
			if v := ansi.Strip(next.(HomeModel).View()); !strings.Contains(v, "saved") {
				t.Fatalf("an older timer cleared the newer note:\n%s", v)
			}
			next, _ = next.Update(messageExpiredMsg{shown: 2})
			if v := ansi.Strip(next.(HomeModel).View()); strings.Contains(v, "saved") {
				t.Errorf("the note should be gone after its time:\n%s", v)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.fn(t) })
	}
}

var errAlreadyRunning = errTest("another wandersort is already running")

type errTest string

func (e errTest) Error() string { return string(e) }

func TestHomeFolderPicker(t *testing.T) {
	dir := t.TempDir()
	m := NewHomeModel(HomeConfig{PickFolder: func() (string, error) { return dir, nil }})

	if v := viewText(m.View()); !strings.Contains(v, "ctrl+o choose a folder") {
		t.Errorf("the footer should offer the folder window:\n%s", v)
	}
	if v := viewText(NewHomeModel(HomeConfig{}).View()); strings.Contains(v, "ctrl+o") {
		t.Errorf("no folder window on this system, so no hint:\n%s", v)
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if cmd == nil {
		t.Fatal("ctrl+o should open the folder window")
	}
	picked := cmd()

	next, _ := m.Update(picked)
	if got := next.(HomeModel).added; len(got) != 1 || got[0] != dir {
		t.Errorf("added = %v, want the chosen folder %s", got, dir)
	}

	next, _ = m.Update(folderPickedMsg{})
	if got := next.(HomeModel).added; len(got) != 0 {
		t.Errorf("a closed window must add nothing, got %v", got)
	}
}

// Repeated ctrl+o while the folder window is open must not open another.
func TestHomeFolderPickerOpensOneWindowAtATime(t *testing.T) {
	opened := 0
	m := NewHomeModel(HomeConfig{PickFolder: func() (string, error) { opened++; return "", nil }})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = next.(HomeModel)
	next, again := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = next.(HomeModel)
	if again != nil {
		t.Fatal("a second ctrl+o opened another window")
	}
	if v := viewText(m.View()); strings.Contains(v, "ctrl+o") {
		t.Errorf("the hint should go while the window is open:\n%s", v)
	}

	picked := cmd()
	if opened != 1 {
		t.Fatalf("windows opened = %d, want 1", opened)
	}
	next, _ = m.Update(picked)
	m = next.(HomeModel)
	if _, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO}); cmd == nil {
		t.Error("ctrl+o should work again once the window has closed")
	}
}
