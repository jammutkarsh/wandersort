package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/library"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/explain"
	"github.com/jammutkarsh/wandersort/pkg/human"
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
	tabReview
	tabCopy
	tabSettings
	numTabs
)

var tabNames = [numTabs]string{"Add", "Organise", "Copy", "Settings"}

// shellStart is which tab a session opens on, and with what. Every
// full-screen command is the same shell opened on its own tab.
type shellStart struct {
	paths []string // tabScan: scan these immediately instead of asking
	tab   int
	force bool // tabScan: re-read every file from disk (--force)
}

// escWindow is how long after an esc a second one still means quit.
const escWindow = 2 * time.Second

// noticeExpiredMsg ends the tab-bar notice numbered seq, if it is still up.
type noticeExpiredMsg struct{ seq int }

// openSettingsMsg opens the settings tab. A message because Init runs on a
// copy of the model; container changes must go through Update.
type openSettingsMsg struct{}

// msgCmd delivers an already-built message on the next Update tick.
func msgCmd(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }

// shellModel is the routing container behind every full-screen command: a tab
// bar plus one live screen per tab, all kept alive so a scan keeps receiving
// events while another tab is open.
type shellModel struct {
	screens [numTabs]tui.Tab
	ctx     context.Context

	// gate is the getting-ready screen, shown alone until both dependencies
	// are installed; nil after
	gate    tui.Tab
	depsErr error // why the dependencies gave up; the session's exit error

	a        *app
	stopCopy context.CancelFunc // ends the Copy tab's context once a fresh one replaces it

	// pendingScan is a scan refused for overlapping the library; a switch to
	// another library runs it
	pendingScan *tui.StartScanMsg

	libraryBefore string // the library folder the settings tab opened on

	start shellStart

	// lib is the library's state, refreshed where it can change, never per frame
	lib library.State

	tab  int
	w, h int

	opening bool // a review is being built off the UI goroutine
	// scanPlan is Work.Plan when the scan screen started; its prefetched
	// review is stale once a re-plan moves Plan on
	scanPlan uint64

	// notice replaces the library name in the tab bar for escWindow; escArmed
	// is the same window, in which a second esc quits
	notice    string
	noticeSeq int
	escArmed  bool
}

// scanReadyMsg reports whether the library opened for a scan.
type scanReadyMsg struct {
	err   error
	paths []string
	force bool
}

// reviewOpenMsg carries the review screen built for [ctrl+r] on the home
// screen (an existing proposal, no scan this session).
type reviewOpenMsg struct {
	model tui.Tab
	err   error
	plan  uint64 // Work.Plan when the build was asked for
}

// depLabels names each dependency on screen.
var depLabels = map[string]string{
	install.PhaseExiftool: "exiftool",
	install.PhaseLocation: "locationDB",
}

// failedDeps is why each dependency in err failed, by phase.
func failedDeps(err error) map[string]string {
	out := map[string]string{}
	for _, de := range install.Failed(err) {
		out[de.Phase] = de.Reason()
	}
	return out
}

// depsDoneMsg reports the dependency install ending, either way.
type depsDoneMsg struct{ err error }

// runShell is the one full-screen program hosting scan, settings and review.
// start says which tab it opens on.
func (a *app) runShell(start shellStart) error {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan logger.Event, 4096)
	// the shell runs on its own session, whose log also reaches the screen;
	// the run's session has opened nothing, so nothing is left behind in it
	shell := *a
	shell.Session = library.New(a.Configuration(), logger.NewTUI(a.LogFile(), func(e logger.Event) { events <- e }), a.LogFile())
	a = &shell
	// on the way out: stop running work, wait for it, then flush and close
	defer func() {
		cancel()
		a.Work.CloseAndWait()
		a.closeDBs()
	}()

	gate := tui.NewReadyModel(
		tui.ReadyItem{Phase: install.PhaseExiftool, Label: depLabels[install.PhaseExiftool]},
		tui.ReadyItem{Phase: install.PhaseLocation, Label: depLabels[install.PhaseLocation]},
	)
	m := shellModel{a: a, ctx: ctx, start: start, gate: gate}
	m.screens[tabScan] = a.newHomeScreen(ctx, nil)
	m.lib = a.State(ctx)

	// mouse clicks pick a tab; the wheel scrolls like the arrows
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(os.Stderr), tea.WithReportFocus(), tea.WithMouseCellMotion())
	// started once for the whole session; nothing else shows until it is done
	a.Deps = a.newDeps(
		func(p install.Progress) {
			prog.Send(tui.InstallProgressMsg{Phase: p.Phase, Done: p.Done, Total: p.Total, Ready: p.Ready})
		},
		func(ctx context.Context, next int, err error) error {
			a.Log().Warn("Download failed, asking for a better network", "try", next-1, "error", err)
			retry := make(chan struct{})
			prog.Send(tui.RetryMsg{Failed: failedDeps(err), Next: next, Tries: install.MaxTries, Wait: install.RetryDelay, Go: retry})
			select {
			case <-retry:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	)
	a.Deps.Start(ctx)
	// both goroutines end with the session context; shutdown waits for them
	if done, ok := a.Work.Start(); ok {
		go func() {
			defer done()
			err := waitForDeps(ctx, a.Deps)
			if !errors.Is(err, context.Canceled) {
				prog.Send(depsDoneMsg{err: err})
			}
		}()
	}
	if done, ok := a.Work.Start(); ok {
		go func() {
			defer done()
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

// exitStatus is how the session ended: only a setup that gave up is a failure;
// a quit the user asked for, mid scan or not, is not.
func (m shellModel) exitStatus() error { return m.depsErr }

// Init shows the getting-ready screen; the tabs start once it lifts.
func (m shellModel) Init() tea.Cmd { return m.gate.Init() }

// updateGate routes everything to the getting-ready screen until the dependencies are in.
func (m shellModel) updateGate(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		next, cmd := m.gate.Update(tea.WindowSizeMsg{Width: tui.Inner(msg.Width), Height: msg.Height})
		m.gate = next.(tui.Tab)
		return m, cmd
	case depsDoneMsg:
		if msg.err == nil {
			m.gate = nil
			cmds := []tea.Cmd{m.place(tabScan, m.screens[tabScan]), m.startCmd()}
			// the last run's library opens straight away, so nothing asks for it
			if m.a.DB() == nil && m.a.LibraryExists() {
				if err := m.a.Open(m.ctx); err != nil {
					cmds = append(cmds, m.forward(tabScan, tui.HomeErrMsg{Err: err}))
				}
				m.refresh()
			}
			return m, tea.Batch(cmds...)
		}
		m.depsErr = msg.err
		next, cmd := m.gate.Update(tui.DepsFailedMsg{Failed: failedDeps(msg.err), Tries: install.MaxTries})
		m.gate = next.(tui.Tab)
		return m, cmd
	case tea.MouseMsg:
		var cmds []tea.Cmd
		for _, key := range clickedKeys(m.View(), msg, m.h) {
			var cmd tea.Cmd
			m, cmd = m.pressGateKey(key)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	case tui.Leave:
		if m.depsErr == nil {
			m.depsErr = errors.New("quit before setup finished")
		}
		return m, tea.Quit
	}
	next, cmd := m.gate.Update(msg)
	m.gate = next.(tui.Tab)
	return m, cmd
}

// pressGateKey sends one clicked key to the getting-ready screen.
func (m shellModel) pressGateKey(key string) (shellModel, tea.Cmd) {
	next, cmd := m.updateGate(tui.KeyMsgFor(key))
	return next.(shellModel), cmd
}

// startCmd asks for the starting tab by message.
func (m shellModel) startCmd() tea.Cmd {
	// Init runs on a copy of the model, so a screen placed there would be lost
	switch {
	case len(m.start.paths) > 0:
		// `wandersort add -p …`: paths already given, start the run
		return msgCmd(tui.StartScanMsg{Paths: m.start.paths, Force: m.start.force})
	case m.start.tab == tabSettings:
		return msgCmd(openSettingsMsg{})
	case m.start.tab == tabReview:
		return msgCmd(tui.OpenReviewMsg{})
	case m.start.tab == tabCopy:
		return msgCmd(tui.OpenCopyMsg{})
	}
	return nil
}

func (m shellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// The renderer rewrites only the lines it thinks changed, so a row the
	// terminal dropped while hidden (another tab in front) stays blank. Coming
	// back repaints everything at the size the window has now.
	if _, ok := msg.(tea.FocusMsg); ok {
		return m, tea.Batch(tea.ClearScreen, tea.WindowSize())
	}
	if m.gate != nil {
		return m.updateGate(msg)
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		// The container owns the tab-bar line and the gutters; every screen
		// lays out below and between them.
		return m, m.broadcast(tea.WindowSizeMsg{Width: tui.Inner(msg.Width), Height: msg.Height - 1})

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tui.SwitchMsg:
		return m.handleSwitch(msg)

	case tui.Leave:
		return m.handleLeave(msg)

	case tui.StartScanMsg:
		if m.replanning() {
			return m, msgCmd(scanReadyMsg{err: library.ErrReplanning})
		}
		// opened here, before the scan screen: opening is quick (the lock never waits)
		if err := m.a.Open(m.ctx); err != nil {
			return m, msgCmd(scanReadyMsg{err: err})
		}
		// a folder overlapping the library is refused before the scan screen
		// replaces the folder list, so the list stays to fix; switching to
		// another library in Settings runs the scan there
		m.pendingScan = nil
		if _, err := workflow.CheckRoots(path.New(), m.a.OutputDir(), msg.Paths); err != nil {
			if errors.Is(err, workflow.ErrOverlapsLibrary) {
				m.pendingScan = &msg
				err = fmt.Errorf("%w, or another library in Settings", err)
			}
			return m, msgCmd(scanReadyMsg{err: err})
		}
		return m, msgCmd(scanReadyMsg{paths: msg.Paths, force: msg.Force})

	case noticeExpiredMsg:
		if msg.seq == m.noticeSeq {
			m.notice, m.escArmed = "", false
		}
		return m, nil

	case tui.NothingNewMsg:
		m.refresh()
		return m, m.homeAgain(msg.Text)

	case tui.OpenReviewMsg:
		if m.copyRunning() {
			return m, m.setNotice("a copy is running — Organise opens when it's done")
		}
		if m.reviewReady() {
			m.tab = tabReview
			return m, nil
		}
		return m, m.openReview()

	case openSettingsMsg:
		return m, m.openSettings()

	case tui.OpenCopyMsg:
		return m, m.openCopy()

	case tui.SettingsSavedMsg:
		if dir := m.a.OutputDir(); dir != m.libraryBefore {
			// another library: screens built over the old one are stale, and
			// its plan is already its own
			m.libraryBefore = dir
			m.screens[tabReview] = nil
			m.screens[tabCopy] = nil
			m.refresh()
			note := m.forward(tabScan, tui.HomeNoteMsg{Text: "Now using the library " + path.New().Friendly(dir)})
			if retry := m.pendingScan; retry != nil {
				m.pendingScan = nil
				return m, tea.Batch(note, msgCmd(*retry))
			}
			return m, note
		}
		// a row of the settings list saved: re-plan if it changed anything
		return m, m.settingsSaved()

	case tui.CopyFinishedMsg:
		// files moved into the library: the counts changed and a kept review
		// would show folders that are now placed
		m.refresh()
		if m.tab != tabReview {
			m.screens[tabReview] = nil
		}
		return m, nil

	case scanReadyMsg:
		if msg.err != nil {
			m.tab = tabScan // a retry from Settings fails where its folders are
			return m, m.forward(tabScan, tui.HomeErrMsg{Err: msg.err})
		}
		// the new scan re-proposes everything, so a prefetched review is stale
		m.screens[tabReview] = nil
		// the scan this starts moves Plan on once (StartJob); its own review
		// is built over that plan
		m.scanPlan = m.a.Work.Plan() + 1
		m.refresh() // the library is open now, so the counts are real
		m.tab = tabScan
		screen := m.a.newScanScreen(m.ctx, msg.paths, msg.force)
		return m, m.place(tabScan, screen)

	case tui.DiscardPlanMsg:
		a, ctx := m.a, m.ctx
		return m, func() tea.Msg {
			n, err := a.Discard(ctx)
			if err != nil {
				return libraryEmptiedMsg{err: fmt.Errorf("discard the current plan: %w", err)}
			}
			return libraryEmptiedMsg{note: "Started over: " + human.Plural(n, "file", "files") + " taken off the list."}
		}

	case libraryEmptiedMsg:
		if msg.err != nil {
			m.tab = tabScan
			return m, m.forward(tabScan, tui.HomeErrMsg{Err: msg.err})
		}
		// the plan is gone: a kept review shows it, and the finished scan's
		// summary counts it
		m.screens[tabReview] = nil
		m.refresh()
		m.tab = tabScan
		cmd := m.place(tabScan, m.a.newHomeScreen(m.ctx, nil))
		m.forward(tabScan, homeMsg(m.lib))
		return m, tea.Batch(cmd, m.forward(tabScan, tui.HomeNoteMsg{Text: msg.note}))

	case replanDoneMsg:
		m.a.settingsToSave = nil // landed or not, the library's own settings are the ones to show
		m.refresh()              // the plan was just rewritten
		if msg.err == nil {
			return m, nil
		}
		cmd := m.forward(tabScan, tui.HomeErrMsg{Err: msg.err})
		if m.screens[tabSettings] != nil {
			// its list still shows what failed to save
			if screen, err := m.a.newSettingsScreen(m.ctx); err == nil {
				cmd = tea.Batch(cmd, m.place(tabSettings, screen))
			}
		}
		return m, cmd

	case reviewOpenMsg:
		m.opening = false
		if msg.plan != m.a.Work.Plan() || m.replanning() {
			return m, nil // built over the plan a re-plan replaced
		}
		if errors.Is(msg.err, tui.ErrNothingNew) {
			return m, m.forward(tabScan, tui.HomeNoteMsg{Text: explain.Error(msg.err)})
		}
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
	if k.String() == "shift+tab" {
		// shift+tab is where "where can I go?" is asked, so refresh here
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
		case m.tab == tabCopy && !m.copyRunning():
			return m, m.openCopy() // fresh numbers every visit
		case m.tab == tabScan:
			if cmd := m.scanTabHome(); cmd != nil {
				return m, cmd
			}
		}
		return m, nil
	}

	if k.String() != "esc" {
		m.notice, m.escArmed = "", false
	}
	// esc esc quits when nothing is running; the first esc still reaches the
	// screen, so it keeps its own meaning (on a running scan or copy: stop)
	if k.String() == "esc" {
		if n, ok := m.screens[m.tab].(tui.Nester); ok && n.Nested() {
			m.escArmed = false // esc closes the sub-menu; it is not a step towards quitting
			return m, m.forward(m.tab, k)
		}
		if m.scanRunning() || m.copyRunning() {
			m.escArmed = false
			return m, tea.Batch(m.forward(m.tab, k), m.setNotice("busy — esc stops it on its own tab"))
		}
		if !m.escArmed {
			m.escArmed = true
			notice := "esc again to quit"
			if m.replanning() {
				notice = "esc again to quit — stops the re-plan, the settings stay as they were"
			}
			return m, tea.Batch(m.forward(m.tab, k), m.setNotice(notice))
		}
		return m, tea.Quit
	}

	// ctrl+c goes to a running scan first (warn once, then cancel). Other
	// screens answer it with a tui.Leave, handled below.
	if k.String() == "ctrl+c" {
		switch {
		case m.scanRunning():
			m.tab = tabScan
		case m.copyRunning():
			m.tab = tabCopy
		}
	}
	return m, m.forward(m.tab, k)
}

// handleMouse opens a clicked tab, presses the keys of a clicked hint or
// choice, and turns the wheel into ↑/↓ for the screen.
func (m shellModel) handleMouse(e tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch {
	case e.Button == tea.MouseButtonWheelUp:
		return m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	case e.Button == tea.MouseButtonWheelDown:
		return m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	case e.Action != tea.MouseActionRelease || e.Button != tea.MouseButtonLeft:
		return m, nil
	case e.Y != 0:
		var cmds []tea.Cmd
		var model tea.Model = m
		for _, key := range clickedKeys(m.View(), e, m.h) {
			var cmd tea.Cmd
			model, cmd = model.(shellModel).handleKey(tui.KeyMsgFor(key))
			cmds = append(cmds, cmd)
		}
		return model, tea.Batch(cmds...)
	}
	t, ok := m.tabAt(e.X - tui.Gutter)
	if !ok || t == m.tab {
		return m, nil
	}
	// shift+tab from the tab before it lands on it, if it can be used now
	before := m
	before.tab = (t + numTabs - 1) % numTabs
	if before.nextTab() != t {
		return m, nil
	}
	return before.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})
}

// clickedKeys is the keys of the hint or choice drawn under a left click on
// view. A view taller than the terminal shows its bottom rows.
func clickedKeys(view string, e tea.MouseMsg, height int) []string {
	if e.Action != tea.MouseActionRelease || e.Button != tea.MouseButtonLeft {
		return nil
	}
	shown := max(strings.Count(view, "\n")+1-height, 0)
	if height <= 0 {
		shown = 0
	}
	target, ok := tui.ClickAt(tui.ClickTargets(view), e.Y+shown, e.X)
	if !ok {
		return nil
	}
	return target.Keys
}

// tabAt is the tab drawn at column x of the top line.
func (m shellModel) tabAt(x int) (int, bool) {
	at := lipgloss.Width(tui.Brand())
	for i, part := range m.tabParts() {
		at += 2 // the join
		w := lipgloss.Width(part)
		if x >= at && x < at+w {
			return i, true
		}
		at += w
	}
	return 0, false
}

// setNotice shows text in the tab bar and starts the window a second esc counts in.
func (m *shellModel) setNotice(text string) tea.Cmd {
	m.notice = text
	m.noticeSeq++
	seq := m.noticeSeq
	return tea.Tick(escWindow, func(time.Time) tea.Msg { return noticeExpiredMsg{seq} })
}

// handleLeave acts on a screen handing back. Screens never call tea.Quit: the
// container owns the program, and quitting would kill the other tabs.
func (m shellModel) handleLeave(l tui.Leave) (tea.Model, tea.Cmd) {
	// Quit first: a scan can hand back asynchronously from a tab the user
	// isn't on, so this must not be read as the current tab finishing.
	if l.Quit {
		return m, tea.Quit // a re-plan cut short leaves the old settings and plan
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
			cmd = m.settingsSaved()
		}
	case tabReview:
		// review edits live in the draft; the home screen says where they go
		// next, but only if there are any
		m.refresh()
		if m.lib.HasEdits() {
			note = "Your edits are saved. Copy uses them."
			m.a.Log().Info(note, logger.UserKey, true)
		}
		m.screens[tabReview] = nil
	case tabCopy:
		m.screens[tabCopy] = nil
		m.refresh()
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

// settingsSaved saves what a settings form handed over: a changed setting
// re-plans the library at once and drops any stashed review screen (its folder
// IDs are gone). The settings tab is unreachable during a scan, so no workflow
// is retargeted.
func (m *shellModel) settingsSaved() tea.Cmd {
	// only the home screen shows the receipt, so it is lost if a
	// finished scan screen still holds the tab
	cmd := m.forward(tabScan, tui.HomeNoteMsg{Text: "Settings saved."})

	s := m.a.settingsToSave
	if s == nil || (m.a.DB() != nil && s.Equal(m.a.Settings())) {
		m.a.settingsToSave = nil
		return cmd // a visit that changed nothing throws no plan away
	}
	m.screens[tabReview] = nil
	return tea.Batch(cmd, m.saveSettings(*s))
}

// libraryEmptiedMsg reports a discarded plan: what to tell the user, or why
// it failed.
type libraryEmptiedMsg struct {
	err  error
	note string
}

// replanDoneMsg reports a settings save ending; only a failure says anything.
type replanDoneMsg struct{ err error }

// saveSettings saves s into the library with the plan rebuilt from it, off the
// UI goroutine.
func (m *shellModel) saveSettings(s config.Settings) tea.Cmd {
	a, ctx := m.a, m.ctx
	return func() tea.Msg {
		if err := a.SaveSettings(ctx, s); err != nil {
			// logged as well as shown: the library keeps the settings its plan was built from
			a.Log().Warn("Settings not saved; the library keeps its old settings and plan. Save them again.",
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
	// kept, and the tab bar marks it; opened only when the user picked it
	m.refresh() // the scan that produced it is done
	if m.a.Work.Plan() != m.scanPlan || m.replanning() {
		return m, nil // built over the plan a re-plan replaced
	}
	cmd := m.place(tabReview, msg.Next)
	if msg.Open {
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
	var history, added []string
	if s, ok := m.screens[tabScan].(tui.ScanModel); ok {
		history = s.Summary()
		if s.Failed() {
			added = s.Paths() // to fix and plan again
		}
	}
	if note != "" {
		history = append(history, note)
	}
	home := m.a.newHomeScreen(m.ctx, history)
	home.Add(added...)
	cmd := m.place(tabScan, home)
	m.forward(tabScan, homeMsg(m.lib))
	return cmd
}

// nextTab cycles the tabs, skipping any that can't be used now.
func (m shellModel) nextTab() int {
	for i := 1; i <= numTabs; i++ {
		t := (m.tab + i) % numTabs
		switch {
		case t == tabReview && !m.canReview():
		case t == tabCopy && (m.planChanging() || (m.screens[tabCopy] == nil && m.lib.Planned == 0)):
		case t == tabSettings && (m.planChanging() || m.copyRunning()):
		case t == tabScan && m.copyRunning():
		default:
			return t
		}
	}
	return m.tab
}

// reviewReady reports whether a built review screen is stashed in its tab.
func (m shellModel) reviewReady() bool { return m.screens[tabReview] != nil }

// canReview reports whether the review tab can be entered: a stashed screen or
// a plan on disk, and never while a scan or re-plan is about to replace that
// plan or a copy is applying the draft (an edit then would be lost).
func (m shellModel) canReview() bool {
	if m.planChanging() || m.copyRunning() {
		return false
	}
	return m.reviewReady() || m.lib.CanReview()
}

// refresh re-reads the library state after something that can change it (a
// scan opening it, a review closing, a re-plan).
func (m *shellModel) refresh() {
	m.lib = m.a.State(m.ctx)
	m.forward(tabScan, homeMsg(m.lib))
}

// homeMsg is the library state as the Add tab shows it.
func homeMsg(s library.State) tui.HomeLibraryMsg {
	return tui.HomeLibraryMsg{Planned: s.Planned, Placed: s.Placed, Edits: s.Edits}
}

// openSettings places the settings wizard, seeded from the library's own
// settings (opening an existing library first). Rebuilt on every entry.
func (m *shellModel) openSettings() tea.Cmd {
	if m.replanning() {
		return m.forward(tabScan, tui.HomeErrMsg{Err: library.ErrReplanning})
	}
	screen, err := m.a.newSettingsScreen(m.ctx)
	if err != nil {
		return m.forward(tabScan, tui.HomeErrMsg{Err: err})
	}
	m.libraryBefore = m.a.OutputDir()
	m.tab = tabSettings
	return m.place(tabSettings, screen)
}

// openReview builds the review over the database off the UI goroutine (lock,
// DB open and BuildTree are slow).
func (m *shellModel) openReview() tea.Cmd {
	if m.replanning() {
		return m.forward(tabScan, tui.HomeErrMsg{Err: library.ErrReplanning})
	}
	if m.opening {
		return nil // a second shift+tab while the first is still building
	}
	m.opening = true
	a, ctx, plan := m.a, m.ctx, m.a.Work.Plan()
	// open on the UI goroutine (see StartScanMsg); building the tree is the
	// slow part and runs off it
	if err := a.Open(ctx); err != nil {
		return msgCmd(reviewOpenMsg{err: err, plan: plan})
	}
	return func() tea.Msg {
		done, ok := a.Work.StartOnLibrary()
		if !ok {
			return nil
		}
		defer done()
		model, err := a.newReviewScreen(ctx)
		return reviewOpenMsg{model: model, err: err, plan: plan}
	}
}

// copyRunning asks the copy tab whether it is busy.
func (m shellModel) copyRunning() bool {
	s := m.screens[tabCopy]
	return s != nil && s.Busy()
}

// openCopy places a fresh Copy tab, unless a copy is already running there.
func (m *shellModel) openCopy() tea.Cmd {
	if m.copyRunning() {
		m.tab = tabCopy
		return nil
	}
	if m.replanning() {
		return m.forward(tabScan, tui.HomeErrMsg{Err: library.ErrReplanning})
	}
	screen, stop, err := m.a.newCopyScreen(m.ctx)
	if err != nil {
		return m.forward(tabScan, tui.HomeErrMsg{Err: err})
	}
	if m.stopCopy != nil {
		m.stopCopy() // the screen it belonged to is not running and never runs again
	}
	m.stopCopy = stop
	m.tab = tabCopy
	return m.place(tabCopy, screen)
}

// scanRunning asks the scan tab whether it is busy.
func (m shellModel) scanRunning() bool {
	s := m.screens[tabScan]
	return s != nil && s.Busy()
}

// planChanging reports whether a scan or a settings re-plan is rewriting the plan.
func (m shellModel) planChanging() bool { return m.scanRunning() || m.replanning() }

// replanning reports a settings save handed over or running: the plan is about
// to be replaced. Set on the UI goroutine, so the very next message finds it.
func (m shellModel) replanning() bool { return m.a.settingsToSave != nil || m.a.Work.Rewriting() }

// place installs a new screen, sized to the current window, and inits it.
func (m *shellModel) place(tab int, s tui.Tab) tea.Cmd {
	sized, cmd := s.Update(tea.WindowSizeMsg{Width: tui.Inner(m.w), Height: m.h - 1})
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
	if m.gate != nil {
		return tui.Frame(m.gate.View())
	}
	s := m.screens[m.tab]
	if s == nil {
		return tui.Frame(m.tabBar())
	}
	return tui.Frame(m.tabBar() + "\n" + s.View())
}

// tabBar is the shell's one line: the app's name, the tabs (● where something waits) and the library.
func (m shellModel) tabBar() string {
	parts := append([]string{tui.Brand()}, m.tabParts()...)
	parts = append(parts, tui.FaintTxt.Render("shift+tab"))
	right := ""
	switch {
	case m.notice != "":
		right = tui.Attn.Render(m.notice)
	case m.a.DB() != nil || m.lib.Exists:
		right = tui.FaintTxt.Render(filepath.Base(m.a.OutputDir()))
	}
	return tui.Row(strings.Join(parts, "  "), right, tui.Inner(m.w))
}

// tabParts is each tab as the top line draws it.
func (m shellModel) tabParts() []string {
	var parts []string
	for i, name := range tabNames {
		switch {
		case i == m.tab:
			parts = append(parts, tui.Pill(name))
		case i == tabReview && m.opening:
			parts = append(parts, tui.DimText.Render(" "+name+"… "))
		case i == tabReview && m.canReview():
			parts = append(parts, tui.Text.Render(" "+name+" ")+tui.Title.Render("●"))
		case i == tabReview:
			parts = append(parts, tui.FaintTxt.Render(" "+name+" "))
		case i == tabCopy && (m.copyRunning() || m.lib.Planned > 0):
			parts = append(parts, tui.Text.Render(" "+name+" ")+tui.Title.Render("●"))
		case i == tabCopy:
			parts = append(parts, tui.FaintTxt.Render(" "+name+" "))
		default:
			parts = append(parts, tui.DimText.Render(" "+name+" "))
		}
	}
	return parts
}

/* --- screen constructors --- */

// newHomeScreen builds the folder-input screen.
func (a *app) newHomeScreen(ctx context.Context, lastScan []string) tui.HomeModel {
	paths := path.New()
	var pickFolder func() (string, error)
	if tui.CanPickFolder() {
		pickFolder = func() (string, error) { return tui.PickFolder(ctx) }
	}
	return tui.NewHomeModel(tui.HomeConfig{
		Suggest:    func(typed string) []string { return suggestDirs(paths, typed) },
		LastScan:   lastScan,
		History:    a.Configuration().SourceHistory(),
		PickFolder: pickFolder,
		// only the library overlap: anything else is the scan's to report
		Check: func(dir string) error {
			if _, err := workflow.CheckRoots(paths, a.OutputDir(), []string{dir}); errors.Is(err, workflow.ErrOverlapsLibrary) {
				return err
			}
			return nil
		},
	})
}

// newScanScreen wires a scan of paths into the shell.
func (a *app) newScanScreen(session context.Context, paths []string, force bool) tui.ScanModel {
	// the scan's own context: its ctrl+c must not cancel the session's
	ctx, cancel := context.WithCancel(session)
	return tui.NewScanModel(tui.ScanConfig{
		Paths:   paths,
		LogPath: path.New().Friendly(a.LogFile().Path()),
		Pipeline: func() error {
			_, err := a.Scan(ctx, paths, force, a.workflowDeps())
			if errors.Is(err, library.ErrClosing) {
				return context.Canceled // quitting: the screen is going too
			}
			return err
		},
		Cancel: cancel,
		ReviewNext: func() (tui.Tab, error) {
			done, ok := a.Work.StartOnLibrary()
			if !ok {
				return nil, context.Canceled
			}
			defer done()
			return a.newReviewScreen(ctx)
		},
	})
}

// newSettingsScreen is the settings list as a shell tab. An existing library
// is opened first so the list shows (and saves back to) its own settings; with
// none, it shows the defaults and the first save creates the library.
func (a *app) newSettingsScreen(ctx context.Context) (tui.Tab, error) {
	if a.DB() == nil && a.LibraryExists() {
		if err := a.Open(ctx); err != nil {
			return nil, err
		}
	}
	geonames := func() (*location.Resolver, error) { return a.Deps.LocationNow() }
	return tui.NewSettingsModel(a.settingsRows(ctx, geonames),
		"Changes apply to files not copied yet. Copied files stay where they are."), nil
}

// runRoot is bare `wandersort`: the shell, or help with --plain or a piped
// stderr.
func (a *app) runRoot(cmd *cobra.Command) error {
	if !a.isTuiEnabled(cmd) {
		return cmd.Help()
	}
	return a.runShell(shellStart{tab: tabScan})
}
