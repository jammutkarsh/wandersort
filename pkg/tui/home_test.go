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
			if !strings.Contains(v, "~/Raw_JPG") || strings.Contains(v, home+"/Raw_JPG") {
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
			if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
				t.Fatal("enter with no folders should not start a scan")
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
		// A refused folder stays in the input with the reason, not in the list.
		{"CheckRefusesBeforeAdding", func(t *testing.T) {
			dir := t.TempDir()
			m := typeHome(NewHomeModel(HomeConfig{Check: func(string) error { return errBadValue }}), dir)
			if len(m.added) != 0 || m.ti.Value() != dir || m.err == nil {
				t.Errorf("list %v, input %q, err %v; want nothing added, the folder still typed, the reason shown", m.added, m.ti.Value(), m.err)
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
			next, cmd := m.Update(HomeErrMsg{Err: errAlreadyRunning})
			if cmd != nil {
				t.Error("an error message should not quit")
			}
			if !strings.Contains(ansi.Strip(next.(HomeModel).View()), "already running") {
				t.Errorf("the error should render:\n%s", ansi.Strip(next.(HomeModel).View()))
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
