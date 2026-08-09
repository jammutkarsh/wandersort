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
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/internal/review"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// The shell's tabs. The scan slot holds the home screen (folder input) until a
// scan starts and again once a review is finished — it is where a session
// begins and returns to, not a separate mode.
const (
	tabScan = iota
	tabConfig
	tabReview
	numTabs
)

var tabNames = [numTabs]string{"Scan", "Config", "Review"}

// shellStart is which tab a session opens on, and with what. Every full-screen
// entry point goes through the shell — bare `wandersort` (an empty start, the
// folder input), and the `scan`/`config`/`review` subcommands, which are the
// same session opened on their own tab. They used to be three separate
// bubbletea programs hosting one screen each, which meant `wandersort scan`
// could not reach the settings and `wandersort config` could not start a scan:
// the tab bar the shell draws was the only place ctrl+t existed, and naming a
// subcommand was enough to lose it. A subcommand is a starting point now, not
// a smaller app.
type shellStart struct {
	tab     int
	paths   []string // tabScan: scan these immediately instead of asking
	force   bool     // tabScan: re-read every file from disk (--force)
	rebuild bool     // tabReview: re-propose before building the tree (--rebuild)
}

// openConfigMsg opens the settings tab. A message rather than a direct call so
// Init can ask for it: Init runs on a copy of the model, so anything that has
// to mutate the container (the tab, the placed screen) must go through Update.
type openConfigMsg struct{}

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

	screens [numTabs]tea.Model
	tab     int
	start   shellStart

	// rebuild re-proposes on the way into the *first* review this session —
	// `review --rebuild`. It clears itself, so ctrl+t back into a review later
	// opens what is on disk rather than silently re-proposing again.
	rebuild bool

	// wf is the running scan's workflow, kept so a settings save can retarget
	// its vfs phase while it is still going (see configSaved).
	wf *workflow.Workflow

	// reviewReady is "a built review screen is stashed in the tab" — the scan
	// prefetched one, or ctrl+t/ctrl+r built one on demand. It is not the same
	// question as whether review can be entered at all; see canReview.
	reviewReady bool
	opening     bool // a review is being built off the UI goroutine
	quitReq     bool // ctrl+c is waiting on the active screen to let go
	exitErr     error
	w, h        int
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
	model tea.Model
	err   error
}

// runShell is the unified entry point: one full-screen program hosting the
// scan, the settings wizard and the review, so organizing a library is one
// invocation instead of three. start says which tab it opens on — every
// full-screen command lands here, so ctrl+t works from all of them.
func (a *app) runShell(start shellStart) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer func() {
		a.closeDBs()
		if a.outLock != nil {
			a.outLock.Unlock()
		}
	}()

	// Events flow to the program; the forwarding goroutine outlives Run() and
	// exits with the process — the send never deadlocks since the program
	// always drains it.
	events := make(chan logger.Event, 4096)
	tuiLog := logger.NewTUI(a.Config.LogLevel, a.Config.LogFile, func(e logger.Event) { events <- e })
	origLog := a.Log
	a.Log = tuiLog
	defer func() { a.Log = origLog }()

	m := shellModel{a: a, ctx: ctx, cancel: cancel, start: start, rebuild: start.rebuild}
	m.screens[tabScan] = a.newHomeScreen(nil)

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
	if m.exitErr != nil {
		return m.exitErr
	}
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
		// `wandersort scan -p …`: the paths are already answered, so skip the
		// folder input and go straight into the run.
		return tea.Batch(cmd, msgCmd(tui.StartScanMsg{Paths: m.start.paths, Force: m.start.force}))
	case m.start.tab == tabConfig:
		return tea.Batch(cmd, msgCmd(openConfigMsg{}))
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

	case tui.StartScanMsg:
		paths, force := msg.Paths, msg.Force
		a, ctx := m.a, m.ctx
		return m, func() tea.Msg { return scanReadyMsg{paths: paths, force: force, err: a.ensureOutput(ctx)} }

	case tui.OpenReviewMsg:
		return m, m.openReview()

	case openConfigMsg:
		return m, m.openConfig()

	case scanReadyMsg:
		if msg.err != nil {
			return m, m.forward(tabScan, tui.HomeErrMsg{Err: msg.err})
		}
		// A new scan re-proposes the whole hierarchy, so whatever review was
		// prefetched is stale; the new run's vfs phase repopulates it.
		m.screens[tabReview], m.reviewReady = nil, false
		m.tab = tabScan
		var screen tui.ScanModel
		m.wf, screen = m.a.newScanScreen(m.ctx, m.cancel, msg.paths, msg.force)
		return m, m.place(tabScan, screen)

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
		next := m.nextTab()
		if next == tabReview && m.screens[tabReview] == nil {
			// Nothing prefetched: build it from what's on disk. The tab stays
			// put until the screen lands (reviewOpenMsg switches), so there's
			// never a blank frame in between.
			return m, m.openReview()
		}
		m.tab = next
		switch {
		case m.tab == tabConfig && m.screens[tabConfig] == nil:
			return m, m.openConfig()
		case m.tab == tabScan:
			if cmd := m.scanTabHome(); cmd != nil {
				return m, cmd
			}
		}
		return m, nil
	}

	// ctrl+c is a quit request wherever it's pressed. While a scan is running
	// it belongs to the scan screen — it warns once, cancels, and only quits
	// if the user insists. Otherwise it means quit the app, but the key is
	// still forwarded first so the review's unsaved-edits guard gets to warn:
	// quitReq is what turns the screen's answer (Done, or a SwitchMsg) into a
	// quit instead of a walk back to the home screen.
	if k.String() == "ctrl+c" {
		if m.scanRunning() {
			m.tab = tabScan
		} else {
			m.quitReq = true
		}
	} else {
		m.quitReq = false // any other key: the screen stayed, so did the session
	}

	cmd := m.forward(m.tab, k)

	// An embedded form doesn't quit the program — it reports done, and the
	// container puts the user back where the answer is now relevant.
	if fm, ok := m.screens[tabConfig].(tui.FormModel); ok && fm.Done() {
		m.screens[tabConfig] = nil
		if m.quitReq {
			return m, tea.Quit
		}
		if err := fm.Error(); err != nil {
			cmd = tea.Batch(cmd, m.forward(tabScan, tui.HomeErrMsg{Err: err}))
		} else if !fm.IsAborted() {
			cmd = tea.Batch(cmd, m.configSaved())
		}
		if m.tab == tabConfig {
			m.tab = tabScan
			if m.reviewReady {
				m.tab = tabReview
			}
		}
	}
	return m, cmd
}

// configSaved picks up a wizard save without a relaunch: the settings the
// review compares its plan against and the next scan uses are re-resolved
// here, and a scan that is still running has its vfs phase retargeted, so the
// folders it ends up proposing are the ones just asked for.
func (m *shellModel) configSaved() tea.Cmd {
	note, err := m.a.reloadConfig()
	if err != nil {
		return m.forward(tabScan, tui.HomeErrMsg{Err: err})
	}
	if m.wf != nil && m.scanRunning() {
		m.wf.UpdateConfig(m.a.Config)
	}
	// A review already on screen was built before this save, so its own stamp
	// check has been and gone — tell it directly, or the plan it is showing
	// silently stops matching the settings. Only when the settings really
	// moved, though: a trip through the wizard that lands back where it
	// started is not a change, and asking about it is the one thing a
	// full-screen question can't get away with.
	var cmd tea.Cmd
	if m.a.settingsChanged(filepath.Dir(m.a.Config.AppDBPath)) {
		cmd = m.forward(tabReview, review.SettingsChangedMsg{})
	}
	// Confirming the save is the wizard's only receipt now that it closes back
	// into the shell instead of ending the process with a printed line.
	if note == "" {
		note = "Settings saved"
		if p, err := m.a.Config.Exists(); err == nil {
			note = "Settings saved in " + p
		}
	}
	// ponytail: shown on the home screen's error line, so it's lost if a scan
	// is on screen instead. Give HomeModel a note line if that matters.
	cmd = tea.Batch(cmd, m.forward(tabScan, tui.HomeErrMsg{Err: errors.New(note)}))
	return cmd
}

// handleSwitch intercepts the screen-swap message the scan and review screens
// send. A nil Next does not quit: the review handing back means one plan is
// settled, not that the session is over.
func (m shellModel) handleSwitch(msg tui.SwitchMsg) (tea.Model, tea.Cmd) {
	if msg.Next != nil {
		// The scan's prefetched review screen. Jumping to it is right when the
		// user is watching the scan and wrong when they're half-way through a
		// form — the tab bar says it's ready instead.
		m.reviewReady = true
		cmd := m.place(tabReview, msg.Next)
		if m.tab == tabScan {
			m.tab = tabReview
		}
		return m, cmd
	}

	note := ""
	if confirmed, saveErr, ok := review.Outcome(m.screens[tabReview]); ok {
		var err error
		// Reported per review as it finishes, not once at exit: the session
		// outlives every plan it saves.
		if note, err = m.a.reportReviewOutcome(confirmed, saveErr); err != nil {
			m.exitErr = err
			return m, tea.Quit
		}
	}
	m.screens[tabReview], m.reviewReady = nil, false
	// ctrl+c out of the review means quit, not "back to the folder input" —
	// the standalone `review` command ends the process on that key too.
	if m.quitReq {
		return m, tea.Quit
	}
	m.tab = tabScan
	return m, m.homeAgain(note)
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
// nothing to review.
func (m shellModel) nextTab() int {
	for i := 1; i <= numTabs; i++ {
		t := (m.tab + i) % numTabs
		if t == tabReview && !m.canReview() {
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
	return m.reviewReady || m.a.hasProposal()
}

// openConfig places the settings wizard. Built fresh on every entry, so it
// re-seeds from the file the last visit wrote.
func (m *shellModel) openConfig() tea.Cmd {
	m.tab = tabConfig
	return m.place(tabConfig, m.a.newConfigScreen(m.ctx))
}

// openReview builds the review over whatever is in the database, off the UI
// goroutine — the lock, the DB open, an eventual --rebuild and BuildTree are
// all too slow to run in Update. Shared by [ctrl+r] on the home screen,
// [ctrl+t] into an unprefetched review tab, and `wandersort review`.
func (m *shellModel) openReview() tea.Cmd {
	if m.opening {
		return nil // a second ctrl+t while the first is still building
	}
	m.opening = true
	// `review --rebuild` re-proposes on the way into the first review only:
	// after that the reviewer is inside the app, where [R] is how they ask
	// for it — silently re-proposing on every later visit would throw away
	// edits they never asked to lose.
	rebuild := m.rebuild
	m.rebuild = false
	a, ctx := m.a, m.ctx
	return func() tea.Msg {
		if err := a.ensureOutput(ctx); err != nil {
			return reviewOpenMsg{err: err}
		}
		if rebuild {
			// The tree is discarded: newReviewScreen segments and builds its
			// own. What's wanted here is the re-proposal it reads back.
			if _, err := a.rebuildTree(ctx, nil); err != nil {
				return reviewOpenMsg{err: err}
			}
		}
		model, err := a.newReviewScreen(ctx)
		return reviewOpenMsg{model: model, err: err}
	}
}

func (m shellModel) scanRunning() bool {
	s, ok := m.screens[tabScan].(tui.ScanModel)
	return ok && s.Running()
}

// place installs a freshly built screen: hands it the current size so it lays
// out on the first frame instead of after the next resize, then inits it.
func (m *shellModel) place(tab int, s tea.Model) tea.Cmd {
	sized, cmd := s.Update(tea.WindowSizeMsg{Width: m.w, Height: m.h - 1})
	m.screens[tab] = sized
	return tea.Batch(sized.Init(), cmd)
}

func (m *shellModel) forward(tab int, msg tea.Msg) tea.Cmd {
	s := m.screens[tab]
	if s == nil {
		return nil
	}
	next, cmd := s.Update(msg)
	m.screens[tab] = next
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
		Suggest:     func(typed string) []string { return suggestDirs(paths, typed) },
		HasProposal: a.hasProposal(),
		LastScan:    lastScan,
	})
}

// newScanScreen wires a scan of paths into the shell, gated behind the same
// upfront dependency download the scan subcommand uses. The workflow comes
// back with the screen: a settings save mid-run retargets it (configSaved).
func (a *app) newScanScreen(ctx context.Context, cancel context.CancelFunc, paths []string, force bool) (*workflow.Workflow, tui.ScanModel) {
	wf := workflow.NewWorkflow(ctx, a.AppDB, a.Log, a.Config, a.workflowDeps())
	return wf, tui.NewScanModel(tui.ScanConfig{
		Pipeline: func() error {
			if err := waitForDeps(a.Deps); err != nil {
				return &tui.DepsErr{Err: err}
			}
			_, err := wf.RunScan(paths, force)
			return err
		},
		Cancel:     cancel,
		ReviewNext: func() (tea.Model, error) { return a.newReviewScreen(ctx) },
		// The whole point of the shell is to land in review; the session
		// continues afterwards, so there is nothing to ask about.
		AutoReview: true,
	})
}

// newConfigScreen is the settings wizard as a shell tab. Same form the config
// subcommand runs — only the program hosting it differs.
func (a *app) newConfigScreen(ctx context.Context) tea.Model {
	fields, save := a.buildConfigForm(ctx, func() (*location.Resolver, error) {
		return a.Deps.LocationNow()
	})
	fm := tui.NewFormModel(fields, save)
	fm.Embedded = true
	return fm
}

// runRoot is bare `wandersort`. With --plain or a piped stderr there is no
// full-screen app to open, so it keeps printing help as it always did.
func (a *app) runRoot(cmd *cobra.Command) error {
	if !a.isTuiEnabled(cmd) {
		return cmd.Help()
	}
	return a.runShell(shellStart{tab: tabScan})
}
