// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

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
			if got := m.Paths(); len(got) != 1 || got[0] != dir {
				t.Fatalf("Paths() = %v, want [%s]", got, dir)
			}
			m = typeHome(m, dir) // same folder again
			if got := m.Paths(); len(got) != 1 {
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
			if got := m.Paths(); len(got) != 1 || got[0] != sub {
				t.Fatalf("Paths() = %v, want the expanded [%s]", got, sub)
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
			if len(m.Paths()) != 0 {
				t.Fatalf("Paths() = %v, want none added", m.Paths())
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
		// ↑ pulls the most recently added folder straight back into the input
		// to edit — one key, not select-then-enter. ctrl+x drops the last
		// folder outright.
		{"UpArrowEditsTheLastAddedFolder", func(t *testing.T) {
			a, b := t.TempDir(), t.TempDir()
			m := typeHome(typeHome(NewHomeModel(HomeConfig{}), a), b)

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
			m = next.(HomeModel)
			if got := m.Paths(); len(got) != 1 || got[0] != a {
				t.Fatalf("↑ should lift the last folder out of the list, got %v", got)
			}
			if m.ti.Value() != b || !m.ti.Focused() {
				t.Errorf("↑ should refocus the input on %q, got %q focused=%v", b, m.ti.Value(), m.ti.Focused())
			}

			// ctrl+x removes the last folder outright.
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
			m = next.(HomeModel)
			if got := m.Paths(); len(got) != 0 {
				t.Fatalf("ctrl+x should remove the remaining folder, got %v", got)
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
