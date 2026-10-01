package tui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// LogEventMsg carries one logger.Event into the TUI. The scan command forwards
// events from the TUI logger's sink into the program via program.Send.
type LogEventMsg struct{ Event logger.Event }

// scanDoneMsg reports the pipeline goroutine returned.
type scanDoneMsg struct{ err error }

// reviewReadyMsg reports ReviewNext (BuildTree + DB work) finished off the UI
// goroutine — see the "y" case in handleKey for why this can't run inline.
type reviewReadyMsg struct {
	model Tab
	err   error
}

// ScanConfig wires the scan screen to the pipeline.
type ScanConfig struct {
	// Pipeline runs the scan (RunScan) and blocks until it finishes. Its
	// user-facing/stream log lines must reach the screen via LogEventMsg.
	Pipeline func() error
	// Cancel cancels the pipeline context on ctrl+c.
	Cancel context.CancelFunc
	// ReviewNext builds the review screen, switched into as soon as it's ready.
	// nil (or an error) leaves the finished scan on screen.
	ReviewNext func() (Tab, error)
}

// ScanModel is the live scan view: a stage stack with files streaming under
// the running stage, notes under the banner, warnings above the footer.
type ScanModel struct {
	cfg      ScanConfig
	sl       StageList
	notes    []string
	warnings []string
	w, h     int

	// cur is the stage key of the running phase, so a stream line's counts
	// drive that phase's own bar — the stream carries no PhaseKey of its own
	cur string

	state scanState
	err   error // why the run failed

	// review is prefetched once the vfs phase flushes; reviewErr is why it
	// could not be built
	review    Tab
	reviewErr error
}

// scanState is where a scan run is.
type scanState int

const (
	scanRunning    scanState = iota
	scanCancelling           // ctrl+c pressed, waiting for the pipeline to unwind
	scanCancelled            // the pipeline unwound after a ctrl+c
	scanFailed               // the pipeline returned an error
	scanFinished             // succeeded: showing or waiting for the review
)

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
		&Stage{Key: "scan", Name: "Scan"},
		&Stage{Key: "metadata", Name: "Metadata", HasBar: true},
		&Stage{Key: "vfs", Name: "Organize"},
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
		case m.state == scanFinished && m.review != nil:
			return m, Switch(m.review) // the run was waiting on this
		}
		return m, nil
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
		// straight into review, or wait for the prefetch
		if m.review != nil {
			return m, Switch(m.review)
		}
		return m, nil
	}
	return m, m.sl.Update(msg)
}

func (m ScanModel) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	}
	return m, nil
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
		m.warnings = append(m.warnings, warningLine(e))
		return m, nil
	}
	if e.UserFacing {
		m.notes = append(m.notes, e.Message)
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

// warningLine renders a warning with the path it's about — "Unsupported file
// type" alone is useless without knowing which file.
func warningLine(e logger.Event) string {
	for _, k := range []string{"walkingPath", "file", "path", "error"} {
		if v, ok := e.Attrs[k].(string); ok && v != "" {
			return e.Message + "  " + v
		}
	}
	return e.Message
}

func (m ScanModel) View() string {
	top := "\n" + m.viewNotes() + "\n"
	footer := m.footer()

	// The running stage's file tail gets every terminal row the chrome doesn't
	// use, so a tall window shows a long live stream instead of dead space.
	used := lipgloss.Height(top) + m.sl.HeaderLines() + lipgloss.Height(footer) + 2
	body := top + m.sl.View(m.w, max(m.h-used, 3))
	return Screen(body, footer, m.h)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// viewNotes renders the last few milestone lines (session start, resolved
// config) dimmed under the banner.
func (m ScanModel) viewNotes() string {
	notes := m.notes
	if len(notes) > 3 {
		notes = notes[len(notes)-3:]
	}
	var b strings.Builder
	for _, n := range notes {
		b.WriteString(row(FaintTxt.Render(" # ")+DimText.Render(n), "", m.w))
		b.WriteString("\n")
	}
	return b.String()
}

// maxFooterWarnings caps warnings shown above the footer; the rest are counted
// and all are in the log.
const maxFooterWarnings = 4

func (m ScanModel) footer() string {
	var b strings.Builder
	warns := m.warnings
	if len(warns) > maxFooterWarnings {
		b.WriteString(FaintTxt.Render(fmt.Sprintf("… %d earlier warnings (see log file)", len(warns)-maxFooterWarnings)))
		b.WriteString("\n")
		warns = warns[len(warns)-maxFooterWarnings:]
	}
	for _, w := range warns {
		b.WriteString(row(Attn.Render("⚠ "+w), "", m.w))
		b.WriteString("\n")
	}
	switch {
	case m.state == scanFailed:
		b.WriteString(Bad.Render("Scan failed: "))
		b.WriteString(Text.Render(m.err.Error()))
		b.WriteString("\n")
		b.WriteString(Footer(KeyHint("ctrl+c", "quit"), m.w))
	case m.state == scanFinished && m.reviewErr != nil:
		b.WriteString(Bad.Render("Could not open review: "))
		b.WriteString(Text.Render(m.reviewErr.Error()))
		b.WriteString("\n")
		b.WriteString(Footer(KeyHint("ctrl+c", "quit"), m.w))
	case m.state == scanFinished && m.cfg.ReviewNext != nil:
		b.WriteString(OK.Render("✓ Scan complete."))
		b.WriteString("  ")
		b.WriteString(DimText.Render("Opening review…"))
	case m.state == scanFinished:
		// no ReviewNext wired: nothing to switch into
		b.WriteString(OK.Render("✓ Scan complete."))
		b.WriteString("\n")
		b.WriteString(Footer(KeyHint("ctrl+c", "quit"), m.w))
	case m.state == scanCancelling:
		// first ctrl+c cancels, the second quits
		b.WriteString(Attn.Render("⚠ Cancelling the scan — press ctrl+c again to quit now. " +
			"Progress so far is saved; the next run resumes."))
	default:
		b.WriteString(Footer(KeyHint("ctrl+c", "cancel"), m.w))
	}
	return b.String()
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
