package tui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/human"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// NothingNewMsg asks the shell to go back to the folder list with Text once a
// scan has planned nothing to copy.
type NothingNewMsg struct{ Text string }

// LogEventMsg carries one logger.Event into the TUI. The scan command forwards
// events from the TUI logger's sink into the program via program.Send.
type LogEventMsg struct{ Event logger.Event }

// scanDoneMsg reports the pipeline goroutine returned.
type scanDoneMsg struct{ err error }

// reviewReadyMsg reports ReviewNext (BuildTree and DB work) finished off the UI goroutine.
type reviewReadyMsg struct {
	model Tab
	err   error
}

// OpenCopyMsg asks the shell for the Copy tab.
type OpenCopyMsg struct{}

// ScanConfig wires the scan screen to the pipeline.
type ScanConfig struct {
	// Pipeline runs the scan (RunScan) and blocks until it finishes. Its
	// user-facing/stream log lines must reach the screen via LogEventMsg.
	Pipeline func() error
	// Cancel cancels the pipeline context on ctrl+c.
	Cancel context.CancelFunc
	// ReviewNext builds the review screen once the scan has returned; it is
	// handed to the shell, and opened when the user picks it.
	ReviewNext func() (Tab, error)
	// Paths are the folders being added, for the heading.
	Paths []string
	// LogPath is where the run's log is, named when warnings only it holds.
	LogPath string
}

// ScanModel plans folders: a stage per pipeline phase while it runs, then what to do next.
type ScanModel struct {
	sl  StageList
	cfg ScanConfig
	err error // why the run failed

	// review is fetched once the scan returns; reviewErr is why it
	// could not be built; wantReview is a pick of choice 1 waiting on it
	review    Tab
	reviewErr error

	// cur is the stage key of the running phase, so a stream line's counts
	// drive that phase's own bar — the stream carries no PhaseKey of its own
	cur string

	// quitting is a run stopped by ctrl+c: the app ends with it
	quitting bool
	// warnings are the user-facing ones, shown once the run ends; logWarnings
	// counts the rest, which only the log has in full
	warnings []string
	w, h     int

	logWarnings int

	state scanState

	choice     int
	wantReview bool

	showKeys bool
}

// scanState is where a scan run is.
type scanState int

const (
	scanRunning    scanState = iota
	scanCancelling           // esc or ctrl+c pressed, waiting for the pipeline to unwind
	scanCancelled            // the pipeline unwound after a ctrl+c
	scanFailed               // the pipeline returned an error
	scanFinished             // succeeded: asking what next
)

// planChoices is what the finished screen offers, in order.
var planChoices = []string{"Organise folders", "Copy into library", "Add more folders"}

// scanKeys is the plan screen's full key list, behind ?.
var scanKeys = []KeyGroup{
	{"While adding", []KeyLine{
		{"shift+tab", "next tab; adding keeps going"},
		{"esc", "stop; next time picks up from here"},
		{"ctrl+c", "quit now"},
	}},
	{"When the folders are ready", []KeyLine{
		{"↑↓", "move between choices"},
		{"enter", "go"},
	}},
}

// Running reports that the pipeline hasn't returned yet.
func (m ScanModel) Running() bool {
	return m.state == scanRunning || m.state == scanCancelling
}

// Busy is the container's word for Running: a pipeline in flight must not be
// interrupted, replaced, or have its settings retargeted under it.
func (m ScanModel) Busy() bool { return m.Running() }

// Nested reports the key list up, or a failed run whose esc goes back to the folders.
func (m ScanModel) Nested() bool { return m.showKeys || m.state == scanFailed }

// Failed reports that the pipeline returned an error; this screen is the only
// place it is shown, so don't discard it unread.
func (m ScanModel) Failed() bool { return m.state == scanFailed }

// Stopped reports a run the user ended before it was done.
func (m ScanModel) Stopped() bool { return m.state == scanCancelled }

// Paths is the folders this run was asked to plan.
func (m ScanModel) Paths() []string { return m.cfg.Paths }

// Summary is each finished stage's one-line result, for the home screen's
// history block once the session moves on from this scan.
func (m ScanModel) Summary() []string { return m.sl.Summary() }

// NewScanModel builds the scan screen; stage keys match logger.PhaseKey.
func NewScanModel(cfg ScanConfig) ScanModel {
	sl := NewStageList(
		nil,
		&Stage{Key: "scan", Name: "Find files"},
		&Stage{Key: "metadata", Name: "Read dates and places", HasBar: true},
		&Stage{Key: "vfs", Name: "Sort into folders"},
	)
	return ScanModel{cfg: cfg, sl: sl}
}

func (m ScanModel) Init() tea.Cmd {
	return tea.Batch(m.sl.Init(), func() tea.Msg {
		return scanDoneMsg{err: m.cfg.Pipeline()}
	})
}

func (m ScanModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.sl.SetWidth(msg.Width)
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case LogEventMsg:
		return m.handleEvent(msg.Event)
	case reviewReadyMsg:
		m.review, m.reviewErr = msg.model, msg.err
		switch {
		case m.state == scanCancelling && m.reviewErr != nil:
			return m, m.unwound()
		case errors.Is(m.reviewErr, vfs.ErrNothingNew):
			return m, m.nothingNew()
		case m.review == nil:
			return m, nil
		case m.wantReview:
			review := m.review
			return m, func() tea.Msg { return SwitchMsg{Next: review, Open: true} }
		}
		return m, Switch(m.review) // the shell keeps it and marks the tab
	case scanDoneMsg:
		if msg.err != nil {
			m.sl.FinishRemaining(true, "")
			if m.state == scanCancelling {
				m.state = scanCancelled
				return m, m.unwound()
			}
			m.state, m.err = scanFailed, msg.err
			return m, nil
		}
		m.sl.FinishRemaining(false, "done")
		m.state = scanFinished
		// straight into Organise once the review lands; warnings keep this
		// screen up so they're read first
		m.wantReview = len(m.warnings) == 0
		switch {
		// only now: until the scan has returned it still holds the library,
		// and the session refuses a review beside it
		case m.review == nil && m.reviewErr == nil && m.cfg.ReviewNext != nil:
			return m, m.fetchReview()
		case m.wantReview && m.review != nil:
			review := m.review
			return m, func() tea.Msg { return SwitchMsg{Next: review, Open: true} }
		}
		return m, m.nothingNew()
	}
	return m, m.sl.Update(msg)
}

// nothingNew hands back to the folder list when the finished scan planned
// nothing to copy; until the run has finished or the review answered, it waits.
func (m ScanModel) nothingNew() tea.Cmd {
	if m.state != scanFinished || !errors.Is(m.reviewErr, vfs.ErrNothingNew) {
		return nil
	}
	return func() tea.Msg { return NothingNewMsg{Text: vfs.ErrNothingNew.Error()} }
}

func openReview() tea.Msg { return OpenReviewMsg{} }

func (m ScanModel) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.showKeys {
		m.showKeys = false
		return m, nil
	}
	switch k.String() {
	case "esc":
		if m.state != scanRunning {
			break
		}
		return m.stop()
	case "ctrl+c":
		if m.cfg.Cancel != nil {
			m.cfg.Cancel()
		}
		// after the run, or on a second press, quit even if the pipeline
		// won't unwind
		if m.state != scanRunning {
			return m, Left(Leave{Quit: true})
		}
		m.state, m.quitting = scanCancelling, true
		return m, nil
	case "?":
		m.showKeys = true
		return m, nil
	}
	if m.state == scanFailed && k.String() == "esc" {
		return m, Left(Leave{Aborted: true}) // back to the folders, to fix
	}
	if m.state != scanFinished {
		return m, nil
	}
	switch k.String() {
	case "up":
		m.choice = max(m.choice-1, 0)
	case "down":
		m.choice = min(m.choice+1, len(planChoices)-1)
	case "enter":
		return m.choose()
	}
	return m, nil
}

// unwound is where a stopped run goes: ctrl+c quits, esc goes back to the folders.
func (m ScanModel) unwound() tea.Cmd {
	if m.quitting {
		return Left(Leave{Quit: true})
	}
	return func() tea.Msg { return NothingNewMsg{Text: "Stopped. Adding the folders again picks up from here."} }
}

// stop asks the pipeline to wind down; the next run picks up from here.
func (m ScanModel) stop() (tea.Model, tea.Cmd) {
	if m.cfg.Cancel != nil {
		m.cfg.Cancel()
	}
	m.state = scanCancelling
	return m, nil
}

// choose acts on the picked choice.
func (m ScanModel) choose() (tea.Model, tea.Cmd) {
	switch m.choice {
	case 0:
		if m.review == nil && m.reviewErr == nil && m.cfg.ReviewNext != nil {
			m.wantReview = true // opens as soon as the review lands
			return m, nil
		}
		return m, openReview
	case 1:
		return m, func() tea.Msg { return OpenCopyMsg{} }
	}
	return m, Left(Leave{})
}

// fetchReview runs ReviewNext (vfs.BuildTree + DB read) off the UI goroutine.
func (m ScanModel) fetchReview() tea.Cmd {
	return func() tea.Msg {
		model, err := m.cfg.ReviewNext()
		return reviewReadyMsg{model: model, err: err}
	}
}

func (m ScanModel) handleEvent(e logger.Event) (tea.Model, tea.Cmd) {
	// phase transition: route to its stage row; strip " in <elapsed>" so the
	// time shows once, in the right column
	if p, ok := e.Attrs[logger.PhaseKey].(string); ok {
		switch e.Attrs[logger.EventKey] {
		case "start":
			m.cur = p
			m.sl.Start(p, e.Message)
		case "done":
			elapsed, _ := e.Attrs[logger.ElapsedKey].(string)
			m.sl.Done(p, strings.TrimSuffix(e.Message, " in "+elapsed), elapsed)
		}
		return m, nil
	}

	// per-file feed line; metadata lines carry the running count
	if e.Stream {
		if f, ok := e.Attrs["file"].(string); ok {
			m.sl.AddTail(f)
		}
		m.progress(e)
		return m, nil
	}

	// Throttled progress milestones (plain-console lines) still move the bar —
	// they're what's left if stream lines are ever filtered out.
	if _, ok := toInt(e.Attrs["total"]); ok {
		m.progress(e)
		return m, nil
	}

	if e.Level >= slog.LevelWarn {
		if e.UserFacing {
			m.warnings = append(m.warnings, e.Message)
		} else {
			m.logWarnings++
		}
	}
	return m, nil
}

// progress drives the running phase's bar from events carrying counts.
func (m *ScanModel) progress(e logger.Event) {
	total, _ := toInt(e.Attrs["total"])
	if cur, ok := toInt(e.Attrs["extracted"]); ok {
		m.sl.SetProgress(m.cur, cur, total)
	}
}

func (m ScanModel) View() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(row(m.heading(), "", m.w) + "\n\n")
	b.WriteString(m.sl.View(m.w) + "\n\n")

	switch m.state {
	case scanRunning:
		left := TimeLeft(m.sl.Remaining(m.cur))
		if left != "" {
			left += ". "
		}
		b.WriteString(row(DimText.Render(left+"This keeps going if you switch tabs."), "", m.w) + "\n")
	case scanCancelling:
		b.WriteString(row(Attn.Render("⚠ Stopping… Next time picks up from here."), "", m.w) + "\n")
	case scanFailed:
		b.WriteString(errorLines(Bad.Render("Couldn't add the folders: ")+Text.Render(ErrorText(m.err)), m.w) + "\n")
	case scanFinished:
		b.WriteString(m.finishedView())
	}

	view := Screen(b.String(), m.footer(), m.h)
	if m.showKeys {
		return KeyHelp(view, scanKeys, m.w, m.h)
	}
	return view
}

func (m ScanModel) heading() string {
	switch m.state {
	case scanFinished:
		return Text.Bold(true).Render("Folders ready")
	case scanFailed:
		return Bad.Render("Adding stopped")
	}
	names := make([]string, len(m.cfg.Paths))
	for i, p := range m.cfg.Paths {
		names[i] = filepath.Base(p)
	}
	folders := "folders"
	if len(names) == 1 {
		folders = "folder"
	}
	return Text.Bold(true).Render(fmt.Sprintf("Adding %d %s", len(names), folders)) + "  " +
		DimText.Render(strings.Join(names, ", "))
}

// finishedView is the warnings the run gathered, then the numbered choice.
func (m ScanModel) finishedView() string {
	var b strings.Builder
	for _, w := range m.warnings {
		b.WriteString(row(Attn.Render("⚠ "+w), "", m.w) + "\n")
	}
	if m.reviewErr != nil {
		b.WriteString(errorLines(Bad.Render("Couldn't open the folders: ")+Text.Render(ErrorText(m.reviewErr)), m.w) + "\n")
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	for i, label := range planChoices {
		pick := append(MoveKeys(m.choice, i), "enter")
		if i == 0 && m.wantReview {
			label += "  " + DimText.Render("opening…")
		}
		b.WriteString(choiceRow(label, i == m.choice, 0, pick...) + "\n")
	}
	return b.String()
}

func (m ScanModel) footer() string {
	if m.logWarnings > 0 {
		text := fmt.Sprintf("⚠ %s noted while adding", human.Plural(m.logWarnings, "problem", "problems"))
		if m.cfg.LogPath != "" {
			text += ". Details: " + m.cfg.LogPath
		}
		return row(Attn.Render(text), "", m.w) + "\n" + m.keyFooter()
	}
	return m.keyFooter()
}

func (m ScanModel) keyFooter() string {
	switch m.state {
	case scanFinished:
		return Footer(KeyHint("↑↓", "choose")+"   "+KeyHint("enter", planChoices[m.choice])+"   "+MoreKeys(), m.w)
	case scanRunning:
		return Footer(KeyHint("esc", "stop")+"   "+MoreKeys(), m.w)
	case scanCancelling:
		return Footer(KeyHint("ctrl+c", "quit now"), m.w)
	}
	if m.state == scanFailed {
		return Footer(KeyHint("esc", "back"), m.w)
	}
	return Footer(KeyHint("ctrl+c", "quit"), m.w)
}

// --- small helpers ---

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
