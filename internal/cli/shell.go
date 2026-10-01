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

// The shell's tabs, one per verb. Add holds the folder input before and after
// a run.
const (
	tabScan = iota
	tabSettings
	tabReview
	numTabs
)

var tabNames = [numTabs]string{"Add", "Settings", "Organise"}

// shellStart is which tab a session opens on, and with what. Every
// full-screen command is the same shell opened on its own tab.
type shellStart struct {
	tab   int
	paths []string // tabScan: scan these immediately instead of asking
	force bool     // tabScan: re-read every file from disk (--force)
}

// openSettingsMsg opens the settings tab. A message because Init runs on a
// copy of the model; container changes must go through Update.
type openSettingsMsg struct{}

// msgCmd delivers an already-built message on the next Update tick.
func msgCmd(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }

// shellModel is the routing container behind every full-screen command: a tab
// bar plus one live screen per tab, all kept alive so a scan keeps receiving
// events while another tab is open.
type shellModel struct {
	a   *app
	ctx context.Context

	screens [numTabs]tui.Tab
	tab     int
	start   shellStart

	opening bool // a review is being built off the UI goroutine
	w, h    int

	// lib is the library's state, refreshed where it can change, never per frame
	lib libraryState

	// settingsBefore is the library's settings as the wizard opened on them,
	// so a save that changes nothing costs nothing (see settingsSaved).
	settingsBefore config.Settings
}

// scanReadyMsg reports whether the library opened for a scan.
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

// downloadLabels names each install phase for the progress rows.
var downloadLabels = map[string]string{
	install.PhaseExiftool: "exiftool",
	install.PhaseLocation: "Location database",
}

// runShell is the one full-screen program hosting scan, settings and review.
// start says which tab it opens on.
func (a *app) runShell(start shellStart) error {
	// on the way out: stop running work, wait for it, then flush and close
	ctx, cancel := context.WithCancel(context.Background())
	defer a.closeDBs()
	defer a.work.closeAndWait()
	defer cancel()

	events := make(chan logger.Event, 4096)
	tuiLog := logger.NewTUI(a.Config.LogLevel, a.logFile, func(e logger.Event) { events <- e })
	origLog := a.Log
	a.Log = tuiLog
	defer func() { a.Log = origLog }()

	m := shellModel{a: a, ctx: ctx, start: start}
	m.screens[tabScan] = a.newHomeScreen(nil)
	m.lib = a.readState(ctx)

	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(os.Stderr))
	// started once for the whole session; every scan reuses it
	a.Deps = a.newDeps(func(phase string, done, total int64) {
		label := downloadLabels[phase]
		prog.Send(tui.InstallProgressMsg{Phase: phase, Label: label, Done: done, Total: total})
		// the settings wizard gets the location download as its own progress
		// row
		if phase == install.PhaseLocation {
			prog.Send(tui.DownloadMsg{Label: label, Done: done, Total: total})
		}
	})
	a.Deps.Start(ctx)
	// both goroutines end with the session context; shutdown waits for them
	if a.work.start() {
		go func() {
			defer a.work.done()
			// the database resolving is when the wizard's progress row settles
			if _, err := a.Deps.Location(ctx); !errors.Is(err, context.Canceled) {
				prog.Send(tui.DownloadMsg{Finished: true})
			}
		}()
	}
	if a.work.start() {
		go func() {
			defer a.work.done()
			for {
				select {
				case e := <-events:
					prog.Send(tui.LogEventMsg{Event: e})
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	final, err := prog.Run()
	if err != nil {
		return err
	}
	if sm, ok := final.(shellModel); ok {
		return sm.exitStatus()
	}
	return nil
}

// exitStatus is how the session ended, read off the screens it kept.
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

// Init boots the home screen, then asks for the starting tab by message (Init
// runs on a copy, so placing a screen here would be lost).
func (m shellModel) Init() tea.Cmd {
	cmd := m.screens[tabScan].Init()
	switch {
	case len(m.start.paths) > 0:
		// `wandersort add -p …`: paths already given, start the run
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
		// opened here, on the UI goroutine, which is the only one that writes
		// the app's library fields; opening is quick (the lock never waits)
		err := m.a.openLibrary(m.ctx)
		return m, msgCmd(scanReadyMsg{paths: msg.Paths, force: msg.Force, err: err})

	case tui.OpenReviewMsg:
		return m, m.openReview()

	case openSettingsMsg:
		return m, m.openSettings()

	case scanReadyMsg:
		if msg.err != nil {
			return m, m.forward(tabScan, tui.HomeErrMsg{Err: msg.err})
		}
		// the new scan re-proposes everything, so a prefetched review is stale
		m.screens[tabReview] = nil
		m.refresh() // the library is open now, so the counts are real
		m.tab = tabScan
		screen := m.a.newScanScreen(m.ctx, msg.paths, msg.force)
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
		m.tab = tabReview
		return m, m.place(tabReview, msg.model)
	}

	// Everything else (log events, install progress, ticks) goes to every live
	// screen. Bubbles ticks carry their own model ID, so foreign ones are
	// dropped.
	return m, m.broadcast(msg)
}

func (m shellModel) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+t" {
		// ctrl+t is where "where can I go?" is asked, so refresh here
		m.refresh()
		next := m.nextTab()
		if next == tabReview && m.screens[tabReview] == nil {
			// nothing prefetched: build it from disk; the tab switches when
			// the screen lands, so there is no blank frame
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

	// ctrl+c goes to a running scan first (warn once, then cancel). Other
	// screens answer it with a tui.Leave, handled below.
	if k.String() == "ctrl+c" && m.scanRunning() {
		m.tab = tabScan
	}
	return m, m.forward(m.tab, k)
}

// handleLeave acts on a screen handing back. Screens never call tea.Quit: the
// container owns the program, and quitting would kill the other tabs.
func (m shellModel) handleLeave(l tui.Leave) (tea.Model, tea.Cmd) {
	// Quit first: a scan can hand back asynchronously from a tab the user
	// isn't on, so this must not be read as the current tab finishing.
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
		// review edits live in the draft; the home screen says where they go
		// next, but only if there are any
		m.refresh()
		if m.lib.HasEdits() {
			note = "Your edits are kept — run 'wandersort execute' to apply them and copy the files."
			m.a.Log.Info(note, logger.UserKey, true)
		}
		m.screens[tabReview] = nil
	}

	// Not a quit: the session goes on. A settled plan or a saved setting
	// means "what next?", which is the scan tab's question.
	if m.tab == tabSettings && m.reviewReady() {
		m.tab = tabReview
		return m, cmd
	}
	m.tab = tabScan
	return m, tea.Batch(cmd, m.homeAgain(note))
}

// settingsSaved applies a wizard save: a changed setting re-plans the library
// at once and drops any stashed review screen (its folder IDs are gone). The
// settings tab is unreachable during a scan, so no workflow is retargeted.
func (m *shellModel) settingsSaved(before config.Settings) tea.Cmd {
	// ponytail: only the home screen shows the receipt, so it is lost if a
	// finished scan screen still holds the tab
	cmd := m.forward(tabScan, tui.HomeNoteMsg{Text: "Settings saved in " + m.a.Config.OutputDir()})

	if m.a.Config.Settings.Equal(before) {
		return cmd // a visit that changed nothing throws no plan away
	}
	m.screens[tabReview] = nil
	return tea.Batch(cmd, m.replan())
}

// replanDoneMsg reports a settings re-plan; only a failure says anything.
type replanDoneMsg struct{ err error }

// replan re-proposes the whole library under the saved settings, off the UI
// goroutine. A failure is logged as well as shown: otherwise execute would copy
// files under settings the user already changed, with only a home-screen line
// (not drawn during a scan) saying so.
func (m *shellModel) replan() tea.Cmd {
	a, ctx := m.a, m.ctx
	return func() tea.Msg {
		if !a.work.start() {
			return nil
		}
		defer a.work.done()
		if _, err := a.rebuildTree(ctx); err != nil {
			a.Log.Warn("Could not re-plan the folders for the new settings — 'wandersort execute' would still copy the old plan. Open the settings and save again.",
				logger.UserKey, true, "error", err)
			return replanDoneMsg{err: err}
		}
		return replanDoneMsg{}
	}
}

// handleSwitch takes the review screen the scan hands over once its plan is
// ready. Screens that are done say so with tui.Leave instead.
func (m shellModel) handleSwitch(msg tui.SwitchMsg) (tea.Model, tea.Cmd) {
	if msg.Next == nil {
		return m, nil // a screen leaving says so with tui.Leave, not with this
	}
	// open it only if the user is watching the scan, never out of a form; the
	// tab bar says it's ready otherwise
	m.refresh() // the scan that produced it is done
	cmd := m.place(tabReview, msg.Next)
	if m.tab == tabScan {
		m.tab = tabReview
	}
	return m, cmd
}

// scanTabHome turns a finished scan's screen back into a folder input when the
// user returns to the tab. A failed run keeps its screen: it shows the reason.
func (m *shellModel) scanTabHome() tea.Cmd {
	s, ok := m.screens[tabScan].(tui.ScanModel)
	if !ok || s.Running() || s.Failed() {
		return nil
	}
	return m.homeAgain("")
}

// homeAgain puts the scan tab back to a folder input, with the finished scan's
// stage summaries above it.
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

// nextTab cycles scan → settings → review → scan, skipping review when there is
// nothing to review and settings while a scan runs.
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

// reviewReady reports whether a built review screen is stashed in its tab.
func (m shellModel) reviewReady() bool { return m.screens[tabReview] != nil }

// canReview reports whether the review tab can be entered: a stashed screen or
// a plan on disk, and never while a scan is about to replace that plan.
func (m shellModel) canReview() bool {
	if m.scanRunning() {
		return false
	}
	return m.reviewReady() || m.lib.CanReview()
}

// refresh re-reads the library state after something that can change it (a
// scan opening it, a review closing, a re-plan).
func (m *shellModel) refresh() {
	m.lib = m.a.readState(m.ctx)
}

// openSettings places the settings wizard, seeded from the library's own
// settings (opening an existing library first). Rebuilt on every entry.
func (m *shellModel) openSettings() tea.Cmd {
	screen, err := m.a.newSettingsScreen(m.ctx)
	if err != nil {
		return m.forward(tabScan, tui.HomeErrMsg{Err: err})
	}
	m.settingsBefore = m.a.Config.Settings
	m.tab = tabSettings
	return m.place(tabSettings, screen)
}

// openReview builds the review over the database off the UI goroutine (lock,
// DB open and BuildTree are slow).
func (m *shellModel) openReview() tea.Cmd {
	if m.opening {
		return nil // a second ctrl+t while the first is still building
	}
	m.opening = true
	a, ctx := m.a, m.ctx
	// open on the UI goroutine (see StartScanMsg); building the tree is the
	// slow part and runs off it
	if err := a.openLibrary(ctx); err != nil {
		return msgCmd(reviewOpenMsg{err: err})
	}
	return func() tea.Msg {
		if !a.work.start() {
			return nil
		}
		defer a.work.done()
		model, err := a.newReviewScreen(ctx)
		return reviewOpenMsg{model: model, err: err}
	}
}

// scanRunning asks the scan tab whether it is busy.
func (m shellModel) scanRunning() bool {
	s := m.screens[tabScan]
	return s != nil && s.Busy()
}

// place installs a new screen, sized to the current window, and inits it.
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

// tabBar is the one line the container owns; it is how ctrl+t is discovered.
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
			// a plan on disk is as ready as a prefetched one
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

// newScanScreen wires a scan of paths into the shell.
func (a *app) newScanScreen(session context.Context, paths []string, force bool) tui.ScanModel {
	// the scan's own context: its ctrl+c must not cancel the session's
	ctx, cancel := context.WithCancel(session)
	wf := workflow.NewWorkflow(a.AppDB, a.Log, a.Config, a.workflowDeps())
	return tui.NewScanModel(tui.ScanConfig{
		Pipeline: func() error {
			if !a.work.start() {
				return context.Canceled
			}
			defer a.work.done()
			if err := waitForDeps(ctx, a.Deps); err != nil {
				return &tui.DepsErr{Err: err}
			}
			_, err := wf.RunScan(ctx, paths, force)
			return err
		},
		Cancel: cancel,
		ReviewNext: func() (tui.Tab, error) {
			if !a.work.start() {
				return nil, context.Canceled
			}
			defer a.work.done()
			return a.newReviewScreen(ctx)
		},
	})
}

// newSettingsScreen is the settings wizard as a shell tab. An existing library
// is opened first so the form is seeded from (and saves back to) its own
// settings; with no library, the form asks for the output path and the save
// creates it.
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

// runRoot is bare `wandersort`: the shell, or help with --plain or a piped
// stderr.
func (a *app) runRoot(cmd *cobra.Command) error {
	if !a.isTuiEnabled(cmd) {
		return cmd.Help()
	}
	return a.runShell(shellStart{tab: a.openingTab()})
}

// openingTab is Settings on a first run (empty library history) and Add after:
// nothing can be planned before the output folder is chosen.
func (a *app) openingTab() int {
	if len(a.Config.History()) == 0 {
		return tabSettings
	}
	return tabScan
}
