package tui

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

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
	// Paths are the folders being planned, for the heading.
	Paths []string
	// Pipeline runs the scan (RunScan) and blocks until it finishes. Its
	// user-facing/stream log lines must reach the screen via LogEventMsg.
	Pipeline func() error
	// Cancel cancels the pipeline context on ctrl+c.
	Cancel context.CancelFunc
	// ReviewNext builds the review screen once the plan is written; it is
	// handed to the shell, and opened when the user picks it.
	ReviewNext func() (Tab, error)
}

// ScanModel plans folders: a stage per pipeline phase while it runs, then what to do next.
type ScanModel struct {
	cfg  ScanConfig
	sl   StageList
	w, h int

	// warnings are the user-facing ones, shown once the run ends; logWarnings
	// counts the rest, which only the log has in full
	warnings    []string
	logWarnings int

	// cur is the stage key of the running phase, so a stream line's counts
	// drive that phase's own bar — the stream carries no PhaseKey of its own
	cur string

	state scanState
	err   error // why the run failed

	// review is prefetched once the vfs phase flushes; reviewErr is why it
	// could not be built; wantReview is a pick of choice 1 waiting on it
	review     Tab
	reviewErr  error
	wantReview bool

	choice   int
	showKeys bool
}

// scanState is where a scan run is.
type scanState int

const (
	scanRunning    scanState = iota
	scanCancelling           // ctrl+c pressed, waiting for the pipeline to unwind
	scanCancelled            // the pipeline unwound after a ctrl+c
	scanFailed               // the pipeline returned an error
	scanFinished             // succeeded: asking what next
)

// planChoices is what the finished screen offers, in order.
var planChoices = []struct{ label, detail string }{
	{"Look over the folders", "rename, merge or flatten before anything is copied"},
	{"Copy as planned", ""},
	{"Add more folders", ""},
}

// scanKeys is the plan screen's full key list, behind ?.
var scanKeys = []KeyGroup{
	{"While planning", []KeyLine{
		{"shift+tab", "next tab; planning keeps going"},
		{"ctrl+c", "stop; the next run carries on"},
	}},
	{"When the plan is ready", []KeyLine{
		{"1-3", "choose what's next"},
		{"↑↓", "move between choices"},
		{"enter", "go"},
	}},
}

// Cancelled reports whether the user's ctrl+c ended the screen.
func (m ScanModel) Cancelled() bool {
	return m.state == scanCancelling || m.state == scanCancelled
}

// Running reports that the pipeline hasn't returned yet.
func (m ScanModel) Running() bool {
	return m.state == scanRunning || m.state == scanCancelling
}

// Busy is the container's word for Running: a pipeline in flight must not be
// interrupted, replaced, or have its settings retargeted under it.
func (m ScanModel) Busy() bool { return m.Running() }

// Failed reports that the pipeline returned an error; this screen is the only
// place it is shown, so don't discard it unread.
func (m ScanModel) Failed() bool { return m.state == scanFailed }

// Summary is each finished stage's one-line result, for the home screen's
// history block once the session moves on from this scan.
func (m ScanModel) Summary() []string { return m.sl.Summary() }

// NewScanModel builds the scan screen; stage keys match logger.PhaseKey.
func NewScanModel(cfg ScanConfig) ScanModel {
	sl := NewStageList(
		nil,
		&Stage{Key: "scan", Name: "Find"},
		&Stage{Key: "metadata", Name: "Read", HasBar: true},
		&Stage{Key: "vfs", Name: "Plan"},
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
			return m, Left(Leave{Quit: true})
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
				return m, Left(Leave{Quit: true})
			}
			m.state, m.err = scanFailed, msg.err
			return m, nil
		}
		m.sl.FinishRemaining(false, "done")
		m.state = scanFinished
		return m, nil
	}
	return m, m.sl.Update(msg)
}

func openReview() tea.Msg { return OpenReviewMsg{} }

func (m ScanModel) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.showKeys {
		m.showKeys = false
		return m, nil
	}
	switch k.String() {
	case "ctrl+c":
		if m.cfg.Cancel != nil {
			m.cfg.Cancel()
		}
		// after the run, or on a second press, quit even if the pipeline
		// won't unwind
		if m.state != scanRunning {
			return m, Left(Leave{Quit: true})
		}
		m.state = scanCancelling
		return m, nil
	case "?":
		m.showKeys = true
		return m, nil
	}
	if m.state != scanFinished {
		return m, nil
	}
	switch k.String() {
	case "up":
		m.choice = max(m.choice-1, 0)
	case "down":
		m.choice = min(m.choice+1, len(planChoices)-1)
	case "1", "2", "3":
		m.choice = int(k.Runes[0] - '1')
	case "enter":
		return m.choose()
	}
	return m, nil
}

// choose acts on the picked choice.
func (m ScanModel) choose() (tea.Model, tea.Cmd) {
	switch m.choice {
	case 0:
		if m.review == nil && m.reviewErr == nil && m.cfg.ReviewNext != nil {
			m.wantReview = true // opens as soon as the prefetch lands
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
			if p == "vfs" && m.cfg.ReviewNext != nil {
				return m, m.fetchReview()
			}
		}
		return m, nil
	}

	// per-file feed line; metadata lines carry the running count
	if e.Stream {
		if f, ok := e.Attrs["file"].(string); ok {
			m.sl.AddTail(f)
		}
		return m, m.progressCmd(e)
	}

	// Throttled progress milestones (plain-console lines) still move the bar —
	// they're what's left if stream lines are ever filtered out.
	if _, ok := toInt(e.Attrs["total"]); ok {
		return m, m.progressCmd(e)
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

// progressCmd drives the running phase's bar from events carrying counts.
func (m *ScanModel) progressCmd(e logger.Event) tea.Cmd {
	total, ok := toInt(e.Attrs["total"])
	if !ok || total <= 0 {
		return nil
	}
	cur, has := toInt(e.Attrs["extracted"])
	if !has {
		return nil
	}
	return m.sl.SetProgress(m.cur, cur, total)
}

func (m ScanModel) View() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(row("  "+m.heading(), "", m.w) + "\n\n")
	b.WriteString(m.sl.View(m.w) + "\n\n")

	switch m.state {
	case scanRunning:
		left := TimeLeft(m.sl.Remaining(m.cur))
		if left != "" {
			left += ". "
		}
		b.WriteString(row("  "+DimText.Render(left+"You can switch tabs; this keeps going."), "", m.w) + "\n")
	case scanCancelling:
		b.WriteString(row("  "+Attn.Render("⚠ Stopping — press ctrl+c again to quit now. The next run carries on from here."), "", m.w) + "\n")
	case scanFailed:
		b.WriteString(row("  "+Bad.Render("Planning failed: ")+Text.Render(m.err.Error()), "", m.w) + "\n")
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
		return Text.Bold(true).Render("Plan ready")
	case scanFailed:
		return Bad.Render("Planning stopped")
	}
	names := make([]string, len(m.cfg.Paths))
	for i, p := range m.cfg.Paths {
		names[i] = filepath.Base(p)
	}
	folders := "folders"
	if len(names) == 1 {
		folders = "folder"
	}
	return Text.Bold(true).Render(fmt.Sprintf("Planning %d %s", len(names), folders)) + "  " +
		DimText.Render(strings.Join(names, ", "))
}

// finishedView is the warnings the run gathered, then the numbered choice.
func (m ScanModel) finishedView() string {
	var b strings.Builder
	for _, w := range m.warnings {
		b.WriteString(row("  "+Attn.Render("⚠ "+w), "", m.w) + "\n")
	}
	if len(m.warnings) == 0 && m.logWarnings > 0 {
		b.WriteString(row("  "+Attn.Render(fmt.Sprintf("⚠ %d warnings — see the log", m.logWarnings)), "", m.w) + "\n")
	}
	if m.reviewErr != nil {
		b.WriteString(row("  "+Bad.Render("Couldn't open the folders: ")+Text.Render(m.reviewErr.Error()), "", m.w) + "\n")
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	for i, c := range planChoices {
		label := c.label
		if i == 0 && m.wantReview {
			label += "  " + DimText.Render("opening…")
		}
		if i == m.choice {
			line := Title.Render(fmt.Sprintf("❯ %d) ", i+1)) + Text.Bold(true).Render(label)
			if c.detail != "" {
				line += "  " + DimText.Render(c.detail)
			}
			b.WriteString(row("  "+line, "", m.w) + "\n")
			continue
		}
		b.WriteString(row("    "+DimText.Render(fmt.Sprintf("%d) ", i+1))+Text.Render(label), "", m.w) + "\n")
	}
	return b.String()
}

func (m ScanModel) footer() string {
	switch m.state {
	case scanFinished:
		return Footer(KeyHint("1-3", "choose")+"   "+KeyHint("enter", "go")+"   "+MoreKeys(), m.w)
	case scanRunning:
		return Footer(KeyHint("shift+tab", "switch tab")+"   "+KeyHint("ctrl+c", "stop")+"   "+MoreKeys(), m.w)
	}
	return Footer(KeyHint("shift+tab", "switch tab")+"   "+KeyHint("ctrl+c", "quit"), m.w)
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
