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
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// The shell's tabs, one per verb. The Add slot holds the folder input until a
// run starts and again once one finishes — it is where a session begins and
// returns to, not a separate mode.
const (
	tabScan = iota
	tabSettings
	tabReview
	numTabs
)

var tabNames = [numTabs]string{"Add", "Settings", "Review"}

// shellStart is which tab a session opens on, and with what. Every full-screen
// entry point goes through the shell — bare `wandersort` (an empty start), and
// the subcommands, which are the same session opened on their own tab. They
// used to be separate bubbletea programs hosting one screen each, which meant
// `wandersort scan` could not reach the settings and `wandersort config` could
// not start a scan: the tab bar the shell draws was the only place ctrl+t
// existed, and naming a subcommand was enough to lose it. A subcommand is a
// starting point now, not a smaller app.
type shellStart struct {
	tab   int
	paths []string // tabScan: scan these immediately instead of asking
	force bool     // tabScan: re-read every file from disk (--force)
}

// openSettingsMsg opens the settings tab. A message rather than a direct call so
// Init can ask for it: Init runs on a copy of the model, so anything that has
// to mutate the container (the tab, the placed screen) must go through Update.
type openSettingsMsg struct{}

// msgCmd delivers an already-built message on the next Update tick.
func msgCmd(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }

// shellModel is the routing container behind every full-screen command: a tab
// bar plus one live screen per tab. It keeps all three screens alive at once,
// which is the point — a scan has to go on receiving its log events while the
// user answers the settings wizard on top of it.
type shellModel struct {
	a      *app
	ctx    context.Context
	cancel context.CancelFunc

	screens [numTabs]tui.Tab
	tab     int
	start   shellStart

	// reviewReady is "a built review screen is stashed in the tab" — the scan
	// prefetched one, or ctrl+t/ctrl+r built one on demand. It is not the same
	// question as whether review can be entered at all; see canReview.
	reviewReady bool
	opening     bool // a review is being built off the UI goroutine
	w, h        int

	// lib is what the library looks like right now, read at the points where
	// it can have changed rather than re-derived per frame. View() used to run
	// a filesystem syscall for the tab bar on every render.
	lib libraryState

	// settingsBefore is the library's settings as the wizard opened on them,
	// so a save that changes nothing costs nothing (see settingsSaved).
	settingsBefore config.Settings
}

// scanReadyMsg reports the output lock + database opened off the UI goroutine,
// so a lock held by another process never blocks the render loop.
type scanReadyMsg struct {
	paths []string
	force bool
	err   error
}

// reviewOpenMsg carries the review screen built for [ctrl+r] on the home
// screen (an existing proposal, no scan this session).
type reviewOpenMsg struct {
	model tui.Tab
	err   error
}

// runShell is the unified entry point: one full-screen program hosting the
// scan, the settings wizard and the review, so organizing a library is one
// invocation instead of three. start says which tab it opens on — every
// full-screen command lands here, so ctrl+t works from all of them.
func (a *app) runShell(start shellStart) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer a.closeDBs()

	// Events flow to the program; the forwarding goroutine outlives Run() and
	// exits with the process — the send never deadlocks since the program
	// always drains it.
	events := make(chan logger.Event, 4096)
	tuiLog := logger.NewTUI(a.Config.LogLevel, a.logFile, func(e logger.Event) { events <- e })
	origLog := a.Log
	a.Log = tuiLog
	defer func() { a.Log = origLog }()

	m := shellModel{a: a, ctx: ctx, cancel: cancel, start: start}
	m.screens[tabScan] = a.newHomeScreen(nil)
	m.lib = a.readState(ctx)

	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(os.Stderr))
	// One eager Start: the Coordinator closes its readiness channels, so it can
	// only ever be started once — every scan in this session reuses it.
	a.Deps = a.newDeps(func(phase string, done, total int64) {
		prog.Send(tui.InstallProgressMsg{Phase: phase, Done: done, Total: total})
		// The settings wizard renders its own progress row and knows nothing
		// about install phases, so the location download is reported to it in
		// its own vocabulary. Without this the config tab is the one screen
		// that never says why the saved-places step is waiting.
		if phase == install.PhaseLocation {
			prog.Send(tui.DownloadMsg{Label: "Location database", Done: done, Total: total})
		}
	})
	a.Deps.Start(ctx)
	// The blocking getter is the completion hook: it returns the moment the
	// database resolves, success or failure, which is exactly when the wizard's
	// bar should settle into its dim "✓ done" line.
	go func() {
		_, _ = a.Deps.Location()
		prog.Send(tui.DownloadMsg{Finished: true})
	}()
	go func() {
		for e := range events {
			prog.Send(tui.LogEventMsg{Event: e})
		}
	}()

	final, err := prog.Run()
	if err != nil {
		return err
	}
	if sm, ok := final.(shellModel); ok {
		return sm.exitStatus()
	}
	return nil
}

// exitStatus is how the session ended, read off the screens the container kept
// — a session has three of them, so "the current screen" is not the answer.
// Review outcomes are reported as each review finishes (see handleSwitch).
func (m shellModel) exitStatus() error {
	if s, ok := m.screens[tabScan].(tui.ScanModel); ok {
		if err := s.DepsFailure(); err != nil {
			return err
		}
		if s.Cancelled() {
			return errors.New("scan cancelled")
		}
	}
	return nil
}

// Init boots the home screen, then asks for whatever tab the command that
// launched this session named. It asks by message rather than doing it here:
// Init runs on a copy, so a screen placed from inside it would be thrown away.
func (m shellModel) Init() tea.Cmd {
	cmd := m.screens[tabScan].Init()
	switch {
	case len(m.start.paths) > 0:
		// `wandersort add -p …`: the paths are already answered, so skip the
		// folder input and go straight into the run.
		return tea.Batch(cmd, msgCmd(tui.StartScanMsg{Paths: m.start.paths, Force: m.start.force}))
	case m.start.tab == tabSettings:
		return tea.Batch(cmd, msgCmd(openSettingsMsg{}))
	case m.start.tab == tabReview:
		return tea.Batch(cmd, msgCmd(tui.OpenReviewMsg{}))
	}
	return cmd
}

func (m shellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		// The container owns the tab-bar line; every screen lays out below it.
		return m, m.broadcast(tea.WindowSizeMsg{Width: msg.Width, Height: msg.Height - 1})

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tui.SwitchMsg:
		return m.handleSwitch(msg)

	case tui.Leave:
		return m.handleLeave(msg)

	case tui.StartScanMsg:
		paths, force := msg.Paths, msg.Force
		a, ctx := m.a, m.ctx
		return m, func() tea.Msg { return scanReadyMsg{paths: paths, force: force, err: a.openLibrary(ctx)} }

	case tui.OpenReviewMsg:
		return m, m.openReview()

	case openSettingsMsg:
		return m, m.openSettings()

	case scanReadyMsg:
		if msg.err != nil {
			return m, m.forward(tabScan, tui.HomeErrMsg{Err: msg.err})
		}
		// A new scan re-proposes the whole hierarchy, so whatever review was
		// prefetched is stale; the new run's vfs phase repopulates it.
		m.screens[tabReview], m.reviewReady = nil, false
		m.refresh() // the library is open now, so the counts are real
		m.tab = tabScan
		screen := m.a.newScanScreen(m.ctx, m.cancel, msg.paths, msg.force)
		return m, m.place(tabScan, screen)

	case replanDoneMsg:
		m.refresh() // the plan was just rewritten
		if msg.err != nil {
			return m, m.forward(tabScan, tui.HomeErrMsg{Err: msg.err})
		}
		return m, nil

	case reviewOpenMsg:
		m.opening = false
		if msg.err != nil {
			return m, m.forward(tabScan, tui.HomeErrMsg{Err: msg.err})
		}
		m.reviewReady = true
		m.tab = tabReview
		return m, m.place(tabReview, msg.model)
	}

	// Everything else — log events, install progress, spinner ticks — goes to
	// every live screen, which is how a scan keeps running underneath the
	// wizard. Bubbletea models ignore messages they don't know, and the
	// bubbles spinner/progress/textinput ticks all carry their own model ID,
	// so a foreign tick is dropped rather than answered with another one.
	return m, m.broadcast(msg)
}

func (m shellModel) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+t" {
		// The one key that asks "where can I go?", so it is the one place
		// worth paying for a fresh answer — a scan that finished underneath
		// the wizard has changed it.
		m.refresh()
		next := m.nextTab()
		if next == tabReview && m.screens[tabReview] == nil {
			// Nothing prefetched: build it from what's on disk. The tab stays
			// put until the screen lands (reviewOpenMsg switches), so there's
			// never a blank frame in between.
			return m, m.openReview()
		}
		m.tab = next
		switch {
		case m.tab == tabSettings && m.screens[tabSettings] == nil:
			return m, m.openSettings()
		case m.tab == tabScan:
			if cmd := m.scanTabHome(); cmd != nil {
				return m, cmd
			}
		}
		return m, nil
	}

	// ctrl+c belongs to a busy tab first: a scan mid-run warns once, cancels,
	// and only gives up if the user insists. Everywhere else the screen still
	// gets the key — it says what leaving means by handing back a tui.Leave,
	// which handleLeave below acts on. The container no longer has to
	// remember that a quit was asked for, because the screen says so.
	if k.String() == "ctrl+c" && m.scanRunning() {
		m.tab = tabScan
	}
	return m, m.forward(m.tab, k)
}

// handleLeave is the one place a screen handing back is acted on. Every
// screen ends the same way now — none of them calls tea.Quit, because the
// container owns the program and a screen that ends it takes the other tabs
// with it, a running scan included. What the hand-back costs is decided here.
func (m shellModel) handleLeave(l tui.Leave) (tea.Model, tea.Cmd) {
	// Quit first, and on its own: a scan can hand back asynchronously from a
	// tab the user isn't on (a dependency download that failed, a cancelled
	// run unwinding), and reading that as "the tab I happen to be looking at
	// just finished" would answer for a screen that said nothing.
	if l.Quit {
		return m, tea.Quit
	}

	var cmd tea.Cmd
	note := ""
	switch m.tab {
	case tabSettings:
		// The wizard is the only hand-back carrying an answer.
		m.screens[tabSettings] = nil
		switch {
		case l.Err != nil:
			cmd = m.forward(tabScan, tui.HomeErrMsg{Err: l.Err})
		case !l.Aborted:
			cmd = m.settingsSaved(m.settingsBefore)
		}
	case tabReview:
		// The review wrote its edits to the draft as they were made; the home
		// screen says where they go next, since the session outlives the
		// review. Only when there are some — a look around that changed
		// nothing has nothing kept to mention.
		m.refresh()
		if m.lib.HasEdits() {
			note = "Review edits kept — run 'wandersort execute' to apply them and copy the files."
			m.a.Log.Info(note, logger.UserKey, true)
		}
		m.screens[tabReview], m.reviewReady = nil, false
	}

	if l.Quit {
		return m, tea.Batch(cmd, tea.Quit)
	}
	// Not a quit: the session goes on. A settled plan or a saved setting
	// means "what next?", which is the scan tab's question.
	if m.tab == tabSettings && m.reviewReady {
		m.tab = tabReview
		return m, cmd
	}
	m.tab = tabScan
	return m, tea.Batch(cmd, m.homeAgain(note))
}

// settingsSaved picks up a wizard save without a relaunch. A changed setting
// re-plans the library at once, no question asked (spec D20): the plan on
// disk was built under settings nobody holds any more, and re-planning
// stored metadata is cheap. Any review screen stashed here is dropped with
// it — its folder IDs are gone — and the next visit to the review tab builds
// one over the new plan. The config tab is unreachable while a scan runs
// (see nextTab/handleKey), so there is never a running workflow to retarget.
func (m *shellModel) settingsSaved(before config.Settings) tea.Cmd {
	// Confirming the save is the wizard's only receipt now that it closes back
	// into the shell instead of ending the process with a printed line.
	// ponytail: shown on the home screen's error line, so it's lost if a scan
	// is on screen instead. Give HomeModel a note line if that matters.
	note := "Settings saved in " + m.a.Config.OutputDir()
	cmd := m.forward(tabScan, tui.HomeErrMsg{Err: errors.New(note)})

	if m.a.Config.Settings.Equal(before) {
		return cmd // a visit that changed nothing throws no plan away
	}
	m.screens[tabReview], m.reviewReady = nil, false
	return tea.Batch(cmd, m.replan())
}

// replanDoneMsg reports the re-plan a settings save triggered; only a failure
// has anything to say, on the home screen's error line.
type replanDoneMsg struct{ err error }

// replan re-proposes the whole library under the settings just saved, off the
// UI goroutine — it re-reads every stored master and rewrites the plan.
//
// A failure is logged as well as shown. `execute` used to refuse a plan built
// under older settings (the stamp compare); with the re-plan happening at the
// save instead, a re-plan that fails silently would leave `execute` copying
// files under settings the user has already changed — and the on-screen half
// of the report is one line on the home screen, which is not even drawn while
// a scan screen is up. The log file always gets it.
func (m *shellModel) replan() tea.Cmd {
	a, ctx := m.a, m.ctx
	return func() tea.Msg {
		if _, err := a.rebuildTree(ctx); err != nil {
			a.Log.Warn("Could not re-plan the folders for the new settings — 'wandersort execute' would still copy the old plan. Open the settings and save again.",
				logger.UserKey, true, "error", err)
			return replanDoneMsg{err: err}
		}
		return replanDoneMsg{}
	}
}

// handleSwitch takes the screen the scan hands over once its plan is ready.
// That is a hand*over*, not a hand-back: a screen that is finished with the
// user says so with tui.Leave, which handleLeave answers.
func (m shellModel) handleSwitch(msg tui.SwitchMsg) (tea.Model, tea.Cmd) {
	if msg.Next == nil {
		return m, nil // a screen leaving says so with tui.Leave, not with this
	}
	// The scan's prefetched review screen. Jumping straight in is right when
	// the user is watching the scan and wrong when they're half-way through a
	// form — the tab bar says it's ready instead.
	m.reviewReady = true
	m.refresh() // the scan that produced it is done
	cmd := m.place(tabReview, msg.Next)
	if m.tab == tabScan {
		m.tab = tabReview
	}
	return m, cmd
}

// scanTabHome turns a finished scan's screen back into a folder input when the
// user returns to the tab: the run is over, so coming back here means "scan
// something else" — needing to quit the app to add a second folder is exactly
// what the unified shell exists to avoid. A *failed* run keeps its screen,
// since that screen is the only place the reason is written.
func (m *shellModel) scanTabHome() tea.Cmd {
	s, ok := m.screens[tabScan].(tui.ScanModel)
	if !ok || s.Running() || s.Failed() {
		return nil
	}
	return m.homeAgain("")
}

// homeAgain puts the scan tab back to a folder input, carrying the finished
// scan's stage summaries above it: organize one folder, then add more without
// leaving the app.
func (m *shellModel) homeAgain(note string) tea.Cmd {
	var history []string
	if s, ok := m.screens[tabScan].(tui.ScanModel); ok {
		history = s.Summary()
	}
	if note != "" {
		history = append(history, note)
	}
	return m.place(tabScan, m.a.newHomeScreen(history))
}

// nextTab cycles scan → config → review → scan, skipping review while there is
// nothing to review and config while a scan is running — settings are
// re-read once, by settingsSaved, and a scan changes what they'd apply to.
func (m shellModel) nextTab() int {
	for i := 1; i <= numTabs; i++ {
		t := (m.tab + i) % numTabs
		if t == tabReview && !m.canReview() {
			continue
		}
		if t == tabSettings && m.scanRunning() {
			continue
		}
		return t
	}
	return m.tab
}

// canReview reports whether the review tab can be entered at all: a screen the
// scan already prefetched, or — the case a prefetch can't cover — a proposal an
// earlier run (or an earlier save this session) left on disk. Not while a scan
// is running: that run replaces the proposal wholesale, so the tree on disk is
// about to be stale, and reviewing it would fight the vfs phase.
func (m shellModel) canReview() bool {
	if m.scanRunning() {
		return false
	}
	return m.reviewReady || m.lib.CanReview()
}

// refresh re-reads the library after something that can have changed it: a
// scan opening it, a review closing, a settings save re-planning. Cheap while
// the library is shut (a stat and the draft file) and one count once it is
// open — but never per frame, which is what View() used to do.
func (m *shellModel) refresh() {
	m.lib = m.a.readState(m.ctx)
}

// openSettings places the settings wizard, seeded from the library's own
// settings — which means opening a library that is already there, since its
// settings are the ones the wizard is about to overwrite. Built fresh on
// every entry, so it re-seeds from whatever the last visit saved.
func (m *shellModel) openSettings() tea.Cmd {
	screen, err := m.a.newSettingsScreen(m.ctx)
	if err != nil {
		return m.forward(tabScan, tui.HomeErrMsg{Err: err})
	}
	m.settingsBefore = m.a.Config.Settings
	m.tab = tabSettings
	return m.place(tabSettings, screen)
}

// openReview builds the review over whatever is in the database, off the UI
// goroutine — the lock, the DB open and BuildTree are all too slow to run in
// Update. Shared by [ctrl+r] on the home screen, [ctrl+t] into an
// unprefetched review tab, `wandersort review`, and a settings-triggered
// re-plan (settingsSaved) swapping in the fresh proposal.
func (m *shellModel) openReview() tea.Cmd {
	if m.opening {
		return nil // a second ctrl+t while the first is still building
	}
	m.opening = true
	a, ctx := m.a, m.ctx
	return func() tea.Msg {
		if err := a.openLibrary(ctx); err != nil {
			return reviewOpenMsg{err: err}
		}
		model, err := a.newReviewScreen(ctx)
		return reviewOpenMsg{model: model, err: err}
	}
}

// scanRunning asks the scan tab whether it is busy. The container needs no
// assertion for this — Busy is on the Tab interface precisely because it is
// the one fact about a screen it cannot work out for itself.
func (m shellModel) scanRunning() bool {
	s := m.screens[tabScan]
	return s != nil && s.Busy()
}

// place installs a freshly built screen: hands it the current size so it lays
// out on the first frame instead of after the next resize, then inits it.
func (m *shellModel) place(tab int, s tui.Tab) tea.Cmd {
	sized, cmd := s.Update(tea.WindowSizeMsg{Width: m.w, Height: m.h - 1})
	m.screens[tab] = sized.(tui.Tab)
	return tea.Batch(sized.Init(), cmd)
}

func (m *shellModel) forward(tab int, msg tea.Msg) tea.Cmd {
	s := m.screens[tab]
	if s == nil {
		return nil
	}
	next, cmd := s.Update(msg)
	m.screens[tab] = next.(tui.Tab)
	return cmd
}

func (m *shellModel) broadcast(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, 0, numTabs)
	for i := range m.screens {
		cmds = append(cmds, m.forward(i, msg))
	}
	return tea.Batch(cmds...)
}

func (m shellModel) View() string {
	s := m.screens[m.tab]
	if s == nil {
		return m.tabBar()
	}
	return m.tabBar() + "\n" + s.View()
}

// tabBar is the one line the container owns, and the whole discoverability
// story for ctrl+t.
func (m shellModel) tabBar() string {
	parts := make([]string, 0, numTabs)
	for i, name := range tabNames {
		switch {
		case i == m.tab:
			parts = append(parts, tui.Selected.Render(" "+name+" "))
		case i == tabReview && m.opening:
			parts = append(parts, tui.DimText.Render(" "+name+" — opening… "))
		case i == tabReview && !m.canReview():
			parts = append(parts, tui.FaintTxt.Render(" "+name+" — waiting for scan "))
		case i == tabReview:
			// canReview, so say so — a plan left on disk by an earlier run is
			// as ready as one this session's scan just prefetched, and the
			// tab bar is the only thing that tells the user it's there.
			parts = append(parts, tui.OK.Render(" "+name+" ✓ ready "))
		default:
			parts = append(parts, tui.DimText.Render(" "+name+" "))
		}
	}
	return tui.Row(strings.Join(parts, tui.FaintTxt.Render("·")),
		tui.KeyHint("ctrl+t", "switch"), m.w)
}

/* --- screen constructors --- */

// newHomeScreen builds the folder-input screen.
func (a *app) newHomeScreen(lastScan []string) tui.HomeModel {
	paths := path.New()
	return tui.NewHomeModel(tui.HomeConfig{
		Suggest:  func(typed string) []string { return suggestDirs(paths, typed) },
		LastScan: lastScan,
	})
}

// newScanScreen wires a scan of paths into the shell, gated behind the same
// upfront dependency download the scan subcommand uses.
func (a *app) newScanScreen(ctx context.Context, cancel context.CancelFunc, paths []string, force bool) tui.ScanModel {
	wf := workflow.NewWorkflow(a.AppDB, a.Log, a.Config, a.workflowDeps())
	return tui.NewScanModel(tui.ScanConfig{
		Pipeline: func() error {
			if err := waitForDeps(a.Deps); err != nil {
				return &tui.DepsErr{Err: err}
			}
			_, err := wf.RunScan(ctx, paths, force)
			return err
		},
		Cancel:     cancel,
		ReviewNext: func() (tui.Tab, error) { return a.newReviewScreen(ctx) },
	})
}

// newSettingsScreen is the settings wizard as a shell tab. Same form the config
// subcommand runs — only the program hosting it differs.
//
// A library that is already there is opened first: the form is seeded with
// its stored settings and the save writes them back, so a visit to the tab
// can never replace one library's rules with another's defaults. A folder
// with no library in it yet stays untouched — the form asks for the output
// path instead, and the save creates it.
func (a *app) newSettingsScreen(ctx context.Context) (tui.Tab, error) {
	if a.AppDB == nil && a.libraryExists() {
		if err := a.openLibrary(ctx); err != nil {
			return nil, err
		}
	}
	fields, save := a.buildSettingsForm(ctx, func() (*location.Resolver, error) {
		return a.Deps.LocationNow()
	})
	return tui.NewFormModel(fields, save), nil
}

// runRoot is bare `wandersort`. With --plain or a piped stderr there is no
// full-screen app to open, so it keeps printing help as it always did.
func (a *app) runRoot(cmd *cobra.Command) error {
	if !a.isTuiEnabled(cmd) {
		return cmd.Help()
	}
	return a.runShell(shellStart{tab: a.openingTab()})
}

// openingTab is Settings on a first run and Add on every one after it.
//
// There is no `wandersort config` any more, so this is the only thing that
// puts a new user in front of the settings — and it has to, because the
// output folder is one of them and nothing can be planned before it is
// answered. A later launch has a library to work in and opens where the work
// is; being asked again for settings that are already saved is the kind of
// front door people stop opening.
//
// "First run" is the library history being empty rather than the current
// folder being unlibraried: someone pointing --output-path at a new folder
// has used WanderSort before and knows where the settings are.
func (a *app) openingTab() int {
	if len(a.Config.History()) == 0 {
		return tabSettings
	}
	return tabScan
}
