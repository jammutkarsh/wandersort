// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// probe is a screen that records every message it is handed, so a test can
// tell "routed to the active tab" from "broadcast to all live tabs".
type probe struct {
	name string
	msgs []tea.Msg
}

func (p *probe) Init() tea.Cmd { return nil }

func (p *probe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	p.msgs = append(p.msgs, msg)
	return p, nil
}

func (p *probe) View() string { return p.name }

// Busy is false: a probe stands in for a screen with nothing in flight.
func (p *probe) Busy() bool { return false }

// got reports whether the screen was handed msg. DeepEqual, not ==: both a
// log event (map of attrs) and a key (rune slice) are uncomparable.
func (p *probe) got(msg tea.Msg) bool {
	return slices.ContainsFunc(p.msgs, func(m tea.Msg) bool { return reflect.DeepEqual(m, msg) })
}

// fakeProposal leaves behind what an earlier run would: a database file where
// the output path says. libraryExists only stats it, so it needn't be a real one.
func fakeProposal(t *testing.T, a *app) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(a.Config.AppDBPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.Config.AppDBPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// finishedScan drives a real ScanModel to the end of a run by executing its
// own Init cmds — the message the screen learns that from is unexported, so
// this runs the real path rather than faking one. A non-nil err fails the run.
func finishedScan(t *testing.T, err error) tui.ScanModel {
	t.Helper()
	m := tui.NewScanModel(tui.ScanConfig{Pipeline: func() error { return err }})
	for _, msg := range flattenTeaCmd(m.Init()) {
		next, _ := m.Update(msg)
		m = next.(tui.ScanModel)
	}
	if m.Running() {
		t.Fatal("the scan should have finished")
	}
	return m
}

// seedProposal leaves a real library behind: a database holding one file that
// is planned and not yet placed. fakeProposal is enough while the library is
// shut — the tab is gated on the file being there — but once the shell opens
// it the count is the answer, and an empty database honestly has nothing to
// review.
func seedProposal(t *testing.T, a *app) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(a.Config.AppDBPath), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := db.New(context.Background(), a.Config.AppDBPath, db.AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, d, 1, "/src", "A.jpg", 5)
	dbtest.SeedEntry(t, d, 1, "/src/A.jpg", "2024/A.jpg")
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

func testShell(t *testing.T) shellModel {
	t.Helper()
	a := &app{Config: testConfig(t), Log: logger.NewNoopLogger()}
	m := shellModel{a: a, ctx: context.Background(), w: 80, h: 24}
	m.screens[tabScan] = a.newHomeScreen(nil)
	return m
}

func TestShellModel(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// The container exists for exactly this: a scan keeps receiving its log
		// events while the settings wizard is on top of it. Anything that isn't
		// a keystroke goes to every live tab.
		{"BroadcastsNonKeyMessagesToEveryLiveTab", func(t *testing.T) {
			m := testShell(t)
			scan, cfg := &probe{name: "scan"}, &probe{name: "config"}
			m.screens[tabScan], m.screens[tabSettings] = scan, cfg
			m.tab = tabSettings

			event := tui.LogEventMsg{Event: logger.Event{Message: "hashing"}}
			m.Update(event)
			if !scan.got(event) || !cfg.got(event) {
				t.Errorf("log events must reach every live screen: scan=%v config=%v",
					scan.got(event), cfg.got(event))
			}
		}},
		// Keystrokes are the opposite: only the tab the user is looking at gets
		// them, or typing in the wizard would drive the review's keymap too.
		{"RoutesKeysToTheActiveTabOnly", func(t *testing.T) {
			m := testShell(t)
			scan, cfg := &probe{name: "scan"}, &probe{name: "config"}
			m.screens[tabScan], m.screens[tabSettings] = scan, cfg
			m.tab = tabSettings

			key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}
			m.Update(key)
			if !cfg.got(key) || scan.got(key) {
				t.Errorf("keys go to the active tab only: config=%v scan=%v", cfg.got(key), scan.got(key))
			}
		}},
		// Review is skipped until a proposal exists — cycling into an empty tab
		// is a dead end the tab bar already explains.
		{"CtrlTSkipsReviewUntilItIsReady", func(t *testing.T) {
			m := testShell(t)
			m.screens[tabSettings] = &probe{name: "config"}

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			m = next.(shellModel)
			if m.tab != tabSettings {
				t.Fatalf("first ctrl+t = tab %d, want config", m.tab)
			}
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			m = next.(shellModel)
			if m.tab != tabScan {
				t.Fatalf("ctrl+t past config with no proposal = tab %d, want scan", m.tab)
			}

			m.reviewReady = true
			m.screens[tabReview] = &probe{name: "review"}
			m.tab = tabSettings
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			if got := next.(shellModel).tab; got != tabReview {
				t.Errorf("ctrl+t with a ready review = tab %d, want review", got)
			}
		}},
		// The scan's prefetched review must never yank a user out of a
		// half-answered form; the tab bar says it's ready instead.
		{"ReadyReviewWaitsWhileTheUserIsInTheWizard", func(t *testing.T) {
			m := testShell(t)
			m.screens[tabSettings] = &probe{name: "config"}
			m.tab = tabSettings

			next, _ := m.Update(tui.SwitchMsg{Next: &probe{name: "review"}})
			m = next.(shellModel)
			if m.tab != tabSettings {
				t.Errorf("switched away from the wizard to tab %d", m.tab)
			}
			if !m.reviewReady {
				t.Error("the review should be marked ready")
			}
			if v := ansi.Strip(m.tabBar()); !strings.Contains(v, "ready") {
				t.Errorf("tab bar should announce the ready review: %q", v)
			}

			// Same message with the scan tab active does switch.
			m.tab, m.reviewReady = tabScan, false
			next, _ = m.Update(tui.SwitchMsg{Next: &probe{name: "review"}})
			if got := next.(shellModel).tab; got != tabReview {
				t.Errorf("with the scan on screen the review should open, got tab %d", got)
			}
		}},
		// Unlike tui.Shell, a nil Next is not "quit": one plan is settled, the
		// session continues with a fresh folder input.
		{"FinishedReviewReturnsHomeInsteadOfQuitting", func(t *testing.T) {
			m := testShell(t)
			m.screens[tabReview], m.reviewReady = &probe{name: "review"}, true
			m.tab = tabReview

			next, cmd := m.Update(tui.Leave{})
			m = next.(shellModel)
			for _, msg := range flattenTeaCmd(cmd) {
				if _, quit := msg.(tea.QuitMsg); quit {
					t.Fatal("a finished review must not quit the app")
				}
			}
			if m.tab != tabScan || m.reviewReady {
				t.Fatalf("want the scan tab with the stale review dropped, got tab=%d ready=%v", m.tab, m.reviewReady)
			}
			if _, ok := m.screens[tabScan].(tui.HomeModel); !ok {
				t.Errorf("the scan tab should hold a fresh home screen, got %T", m.screens[tabScan])
			}
		}},
		// The reported bug: after one scan the tab kept showing that finished
		// run forever, so adding a second folder meant quitting the app —
		// exactly what the unified shell exists to avoid. Coming back to the
		// tab means "scan something else".
		{"ReturningToTheScanTabOffersAnotherScan", func(t *testing.T) {
			m := testShell(t)
			m.screens[tabSettings] = &probe{name: "config"}
			m.screens[tabScan] = finishedScan(t, nil)
			m.tab = tabSettings

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			m = next.(shellModel)
			if m.tab != tabScan {
				t.Fatalf("ctrl+t = tab %d, want scan", m.tab)
			}
			if _, ok := m.screens[tabScan].(tui.HomeModel); !ok {
				t.Fatalf("a finished scan's tab should offer a folder input, got %T", m.screens[tabScan])
			}
			if cmd == nil {
				t.Error("the new home screen needs its Init")
			}
			if v := ansi.Strip(m.View()); !strings.Contains(v, "Add more folders to scan") {
				t.Errorf("the finished run should be summarized above the input:\n%s", v)
			}
		}},
		// A failed run keeps its screen — it is the only place the reason is
		// written, and replacing it with an input throws that away unread.
		{"AFailedScanKeepsItsScreen", func(t *testing.T) {
			m := testShell(t)
			m.screens[tabSettings] = &probe{name: "config"}
			m.screens[tabScan] = finishedScan(t, errors.New("disk went away"))
			m.tab = tabSettings

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			m = next.(shellModel)
			if _, ok := m.screens[tabScan].(tui.HomeModel); ok {
				t.Fatal("a failed scan's screen must survive, error and all")
			}
			if v := ansi.Strip(m.View()); !strings.Contains(v, "disk went away") {
				t.Errorf("the failure should still be readable:\n%s", v)
			}
		}},
		// A lock held by another process is not fatal: it renders on the home
		// screen and the app stays up.
		{"LockFailureRendersOnHome", func(t *testing.T) {
			m := testShell(t)
			next, _ := m.Update(scanReadyMsg{err: errors.New("already running (PID 42)")})
			m = next.(shellModel)
			if v := ansi.Strip(m.View()); !strings.Contains(v, "already running") {
				t.Errorf("want the lock failure on screen:\n%s", v)
			}
		}},
		// The reported bug: a proposal from an earlier run is reachable, but
		// the tab was gated on a screen the *scan* prefetches, so relaunching
		// and cycling only ever said "waiting for scan". The same gate locked
		// the user out after saving, when the prefetched screen is dropped.
		{"ReviewIsReachableFromAProposalOnDisk", func(t *testing.T) {
			m := testShell(t)
			if m.canReview() {
				t.Fatal("no database yet — nothing to review")
			}
			if v := ansi.Strip(m.tabBar()); !strings.Contains(v, "waiting for scan") {
				t.Errorf("tab bar should say why review is closed: %q", v)
			}

			// An earlier run's database, no prefetched screen — exactly the
			// state a relaunch (or a finished save) leaves behind. The library
			// is read at the points where it can have changed rather than per
			// frame, and ctrl+t is one of them.
			seedProposal(t, m.a)
			m.refresh()
			if !m.canReview() {
				t.Fatal("a proposal on disk must be reviewable without scanning again")
			}
			// The tab bar is the only thing that tells the user the plan is
			// there — saying nothing (a plain dim tab) reads as "not yet".
			if v := ansi.Strip(m.tabBar()); !strings.Contains(v, "ready") {
				t.Errorf("tab bar should announce the proposal on disk: %q", v)
			}

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			next, cmd := next.(shellModel).Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			m = next.(shellModel)
			if !m.opening || cmd == nil {
				t.Fatalf("ctrl+t into an unprefetched review should build one, opening=%v", m.opening)
			}
			// The tab only moves once the screen lands, so no blank frame.
			if m.tab == tabReview {
				t.Error("the review tab should not open before its screen exists")
			}
		}},
		// A scan replaces the proposal wholesale, so the tree on disk is about
		// to be stale — reviewing it mid-run would fight the vfs phase.
		{"ReviewIsClosedWhileAScanRuns", func(t *testing.T) {
			m := testShell(t)
			fakeProposal(t, m.a)
			m.refresh()
			m.screens[tabScan] = tui.NewScanModel(tui.ScanConfig{})
			if m.canReview() {
				t.Error("review must stay closed while a scan is running")
			}
		}},
		// ctrl+t builds the real wizard — the same form the config subcommand
		// runs, only hosted here.
		{"ConfigTabOpensTheWizard", func(t *testing.T) {
			m := testShell(t)
			m.a.Deps = m.a.newDeps(nil) // the wizard's geonames peek; never started

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
			m = next.(shellModel)
			if m.tab != tabSettings || m.screens[tabSettings] == nil {
				t.Fatalf("ctrl+t should build and open the wizard, got tab=%d screen=%v", m.tab, m.screens[tabSettings])
			}
			if v := ansi.Strip(m.View()); !strings.Contains(v, "Output path") {
				t.Errorf("the wizard should be on screen:\n%s", v)
			}
		}},
		// A saved wizard hands the tab back instead of taking the program (and
		// the scan under it) down with it. Driven with a stand-in form so the
		// test states one thing — the container's Done handling — rather than
		// the real wizard's field order.
		{"FinishedWizardHandsTheTabBack", func(t *testing.T) {
			m := testShell(t)
			yes := true
			form := tui.NewFormModel([]*tui.Field{{Kind: tui.FieldConfirm, Title: "Only step", BoolValue: &yes}}, nil)
			m.screens[tabSettings] = form
			m.tab = tabSettings
			// What openSettings records when it places the wizard: this stand-in
			// changes nothing, so the save must not kick off a re-plan.
			m.settingsBefore = m.a.Config.Settings

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
			m = next.(shellModel)
			// The key makes the form hand back; the container acts on that.
			next, cmd = m.Update(leaveFrom(t, cmd))
			m = next.(shellModel)
			for _, msg := range flattenTeaCmd(cmd) {
				if _, quit := msg.(tea.QuitMsg); quit {
					t.Fatal("saving the wizard must not quit the app")
				}
			}
			if m.tab != tabScan {
				t.Errorf("leaving the wizard = tab %d, want scan", m.tab)
			}
			if m.screens[tabSettings] != nil {
				t.Error("the finished form should be dropped, so the next visit re-seeds from disk")
			}
		}},
		// The reported bug: ctrl+c anywhere is a quit request, and being
		// dropped back on the folder input instead is not quitting. The
		// standalone `config`/`review` commands both end the process on it.
		{"CtrlCQuitsFromEveryTab", func(t *testing.T) {
			// The screen says whether leaving means the session or just the
			// screen; the container acts on that one message rather than
			// remembering that a quit was asked for.
			t.Run("config", func(t *testing.T) {
				m := testShell(t)
				m.a.Deps = m.a.newDeps(nil)
				next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
				m = next.(shellModel)
				_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
				_, cmd = m.Update(leaveFrom(t, cmd))
				assertQuits(t, cmd, "ctrl+c in the wizard")
			})
			t.Run("review", func(t *testing.T) {
				m := testShell(t)
				m.screens[tabReview], m.reviewReady = &probe{name: "review"}, true
				m.tab = tabReview

				_, cmd := m.Update(tui.Leave{Quit: true})
				assertQuits(t, cmd, "ctrl+c out of the review")
			})
			t.Run("leaving without asking to quit goes home", func(t *testing.T) {
				m := testShell(t)
				m.screens[tabReview], m.reviewReady = &probe{name: "review"}, true
				m.tab = tabReview

				// esc out of the review is "done here", not "done with the
				// app": the session goes on, back at the folder input.
				next, cmd := m.Update(tui.Leave{})
				for _, msg := range flattenTeaCmd(cmd) {
					if _, quit := msg.(tea.QuitMsg); quit {
						t.Fatal("a review left without a quit must return home")
					}
				}
				if next.(shellModel).tab != tabScan {
					t.Errorf("want the scan tab, got %d", next.(shellModel).tab)
				}
			})
		}},
		// Leaving review mentions the kept edits only when the draft holds
		// some — a look around that changed nothing has nothing to point at.
		{"ReviewNoteOnlyWithDraftEdits", func(t *testing.T) {
			leave := func(t *testing.T, withEdit bool) string {
				m := testShell(t)
				m.a.Config.AppDBPath = filepath.Join(t.TempDir(), ".wandersort.db")
				if withEdit {
					if err := vfs.AppendDraft(filepath.Dir(m.a.Config.AppDBPath), vfs.Edit{Seq: 1, Op: vfs.OpRename, Node: 1, To: "x"}); err != nil {
						t.Fatal(err)
					}
				}
				m.screens[tabReview], m.reviewReady = &probe{name: "review"}, true
				m.tab = tabReview
				next, _ := m.Update(tui.Leave{})
				return ansi.Strip(next.(shellModel).View())
			}
			if v := leave(t, false); strings.Contains(v, "Review edits kept") {
				t.Errorf("no edits, yet the home screen says edits were kept:\n%s", v)
			}
			if v := leave(t, true); !strings.Contains(v, "Review edits kept") {
				t.Errorf("draft edits, yet no note on the home screen:\n%s", v)
			}
		}},
		// exitStatus reads the scan model the container kept — tui.Shell's
		// Current() never sees it, since the ScanModel lives inside a tab.
		{"ExitStatusReportsACancelledScan", func(t *testing.T) {
			m := testShell(t)
			sm := tui.NewScanModel(tui.ScanConfig{Cancel: func() {}})
			cancelled, _ := sm.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			m.screens[tabScan] = cancelled.(tui.Tab)

			if err := m.exitStatus(); err == nil || !strings.Contains(err.Error(), "cancelled") {
				t.Errorf("exitStatus() = %v, want a cancellation", err)
			}
		}},
		// The reported bug: `wandersort scan` (and config, and review) hosted
		// one screen with no tab bar, so ctrl+t did nothing and a subcommand
		// was a strictly smaller app than a bare `wandersort`. Each one is now
		// the same session opened on its own tab, which is this routing.
		{"SubcommandStartsOpenTheirOwnTab", func(t *testing.T) {
			for _, tc := range []struct {
				name  string
				start shellStart
				want  tea.Msg
			}{
				{
					"scan",
					shellStart{tab: tabScan, paths: []string{"/pics"}},
					tui.StartScanMsg{Paths: []string{"/pics"}},
				},
				{"config", shellStart{tab: tabSettings}, openSettingsMsg{}},
				{"review", shellStart{tab: tabReview}, tui.OpenReviewMsg{}},
			} {
				m := testShell(t)
				m.start = tc.start
				msgs := flattenTeaCmd(m.Init())
				if !slices.ContainsFunc(msgs, func(got tea.Msg) bool {
					return reflect.DeepEqual(got, tc.want)
				}) {
					t.Errorf("%s: Init() = %v, want it to ask for %#v", tc.name, msgs, tc.want)
				}
			}
		}},
		// …and the tab it asks for is the tab it lands on. Config is the one
		// the container opens itself; scan and review land via their own
		// ready/open messages, so all three are checked through Update.
		{"SubcommandStartsLandOnTheirOwnTab", func(t *testing.T) {
			for _, tc := range []struct {
				name string
				msg  tea.Msg
				want int
			}{
				{"config", openSettingsMsg{}, tabSettings},
				{"review", reviewOpenMsg{model: &probe{name: "review"}}, tabReview},
				{"scan", scanReadyMsg{paths: []string{"/pics"}}, tabScan},
			} {
				m := testShell(t)
				m.tab = tabSettings // somewhere else, so landing is observable
				next, _ := m.Update(tc.msg)
				if got := next.(shellModel).tab; got != tc.want {
					t.Errorf("%s: landed on tab %d, want %d", tc.name, got, tc.want)
				}
			}
		}},
		// A bare `wandersort` opens on the folder input and asks for nothing.
		{"BareStartOpensTheHomeScreenOnly", func(t *testing.T) {
			m := testShell(t)
			for _, msg := range flattenTeaCmd(m.Init()) {
				switch msg.(type) {
				case tui.StartScanMsg, openSettingsMsg, tui.OpenReviewMsg:
					t.Errorf("a bare start should open no tab, got %#v", msg)
				}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

// leaveFrom pulls the hand-back a screen's key produced, so a test can feed
// it to the container the way the program would.
func leaveFrom(t *testing.T, cmd tea.Cmd) tui.Leave {
	t.Helper()
	for _, msg := range flattenTeaCmd(cmd) {
		if l, ok := msg.(tui.Leave); ok {
			return l
		}
	}
	t.Fatalf("no tui.Leave in %v", flattenTeaCmd(cmd))
	return tui.Leave{}
}

func assertQuits(t *testing.T, cmd tea.Cmd, what string) {
	t.Helper()
	for _, msg := range flattenTeaCmd(cmd) {
		if _, quit := msg.(tea.QuitMsg); quit {
			return
		}
	}
	t.Errorf("%s should quit the app, got %v", what, flattenTeaCmd(cmd))
}

func flattenTeaCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, flattenTeaCmd(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// TestConfigSavedReplansOnlyOnAChange: a settings save re-plans at once,
// without asking, but *only* when the settings really moved — a trip through
// the wizard that lands back where it started must not throw away the plan or
// the review edits made against it. The library is open by the time this
// runs: the save itself writes into it (see app.saveSettings), which is why
// the re-plan can go straight to rebuildTree.
func TestConfigSavedReplansOnlyOnAChange(t *testing.T) {
	before := config.Settings{Rules: []string{"date", "location"}, CollapseLevels: true}
	for _, tc := range []struct {
		name       string
		after      config.Settings
		wantReplan bool
	}{
		{"net-zero edit — plan and edits kept", before, false},
		{"changed rules — re-planned", config.Settings{Rules: []string{"device"}, CollapseLevels: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testShell(t)
			m.a.Config.Settings = tc.after
			open := &probe{name: "review"}
			m.screens[tabReview], m.reviewReady = open, true

			// The Cmd is deliberately not run: it re-plans against the open
			// library, which this shell has no Deps or database for. Dropping
			// the stale review screen is the observable half, and the half a
			// ctrl+t can race.
			m.settingsSaved(before)
			if v := ansi.Strip(m.screens[tabScan].View()); !strings.Contains(v, "Settings saved") {
				t.Errorf("the save is only confirmed on the home screen, and it didn't say so:\n%s", v)
			}
			if tc.wantReplan {
				// The drop happens in settingsSaved itself, not inside the Cmd:
				// a ctrl+t before the re-plan finishes must not find a screen
				// built over folder IDs that no longer exist.
				if m.screens[tabReview] != nil || m.reviewReady {
					t.Error("the stale review screen must be dropped before the re-plan runs")
				}
			} else if m.screens[tabReview] != open || !m.reviewReady {
				t.Error("a save that changed nothing must not drop the open review")
			}
		})
	}
}

// With no `wandersort config` any more, the opening tab is the only thing
// that puts a new user in front of the settings — and it has to, because the
// output folder is one of them and nothing can be planned before it is
// answered. A later launch opens where the work is instead.
func TestOpeningTabIsSettingsOnlyOnAFirstRun(t *testing.T) {
	a := &app{Config: testConfig(t), Log: logger.NewNoopLogger()}
	if got := a.openingTab(); got != tabSettings {
		t.Errorf("first run opened tab %d, want Settings (%d)", got, tabSettings)
	}

	// Remembering a library is what makes a run not the first one — the same
	// call openLibrary makes once a folder is known to really be a library.
	lib := t.TempDir()
	if err := a.Config.Remember(lib); err != nil {
		t.Fatal(err)
	}
	if got := a.openingTab(); got != tabScan {
		t.Errorf("second run opened tab %d, want Add (%d)", got, tabScan)
	}
}
