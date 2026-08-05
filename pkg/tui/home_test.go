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
		// ↑ walks up into the folders already added, so a mistyped one is
		// fixable without retyping the rest. ctrl+x there drops the folder
		// under the cursor, not blindly the last one.
		{"UpArrowEditsAndRemovesAnAddedFolder", func(t *testing.T) {
			a, b := t.TempDir(), t.TempDir()
			m := typeHome(typeHome(NewHomeModel(HomeConfig{}), a), b)

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
			m = next.(HomeModel)
			if m.sel != 1 {
				t.Fatalf("↑ from the input = row %d, want the last folder added", m.sel)
			}
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
			m = next.(HomeModel)
			if m.sel != 0 {
				t.Fatalf("a second ↑ = row %d, want the row above", m.sel)
			}
			if m.ti.Focused() {
				t.Error("the text input should be blurred while a folder is selected")
			}

			// ctrl+x removes the selected folder, not the last one.
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
			m = next.(HomeModel)
			if got := m.Paths(); len(got) != 1 || got[0] != b {
				t.Fatalf("Paths() = %v, want only [%s] left", got, b)
			}

			// enter pulls the selected folder back into the input to correct.
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(HomeModel)
			if len(m.Paths()) != 0 {
				t.Errorf("editing should lift the folder out of the list, got %v", m.Paths())
			}
			if m.ti.Value() != b || m.sel != -1 || !m.ti.Focused() {
				t.Errorf("editing should refocus the input on %q, got %q sel=%d focused=%v",
					b, m.ti.Value(), m.sel, m.ti.Focused())
			}
		}},
		// ↓ walks back out of the list, and a typed character always does.
		{"LeavingTheFolderList", func(t *testing.T) {
			m := typeHome(NewHomeModel(HomeConfig{}), t.TempDir())

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
			next, _ = next.(HomeModel).Update(tea.KeyMsg{Type: tea.KeyDown})
			if got := next.(HomeModel).sel; got != -1 {
				t.Errorf("↓ past the last folder = row %d, want back in the input", got)
			}

			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
			next, _ = next.(HomeModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
			typed := next.(HomeModel)
			if typed.sel != -1 || typed.ti.Value() != "x" {
				t.Errorf("typing should leave the list and land in the input, got sel=%d value=%q",
					typed.sel, typed.ti.Value())
			}
		}},
		// Completions come from the injected Suggest; tab fills the top one.
		{"TabFillsTheTopCompletion", func(t *testing.T) {
			m := NewHomeModel(HomeConfig{Suggest: func(string) []string { return []string{"~/Pictures", "~/Public"} }})
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("~/P")})
			m = next.(HomeModel)
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
			m = next.(HomeModel)
			if got := m.ti.Value(); got != "~/Pictures" {
				t.Errorf("tab filled %q, want the top completion", got)
			}
		}},
		// [ctrl+r] only offers itself when there is a proposal to open.
		{"ReviewKeyNeedsAProposal", func(t *testing.T) {
			m := NewHomeModel(HomeConfig{})
			if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR}); cmd != nil {
				t.Error("ctrl+r without a proposal should do nothing")
			}
			m = NewHomeModel(HomeConfig{HasProposal: true})
			_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
			if cmd == nil {
				t.Fatal("ctrl+r with a proposal should ask to open the review")
			}
			if _, ok := cmd().(OpenReviewMsg); !ok {
				t.Errorf("cmd produced %T, want OpenReviewMsg", cmd())
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
