package tui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// DepsErr marks a failure before any phase started (a dependency download).
// The screen quits at once and the caller prints it (see DepsFailure).
type DepsErr struct{ Err error }

func (e *DepsErr) Error() string { return e.Err.Error() }
func (e *DepsErr) Unwrap() error { return e.Err }

// LogEventMsg carries one logger.Event into the TUI. The scan command forwards
// events from the TUI logger's sink into the program via program.Send.
type LogEventMsg struct{ Event logger.Event }

// InstallProgressMsg carries dependency-download byte progress, straight from a
// callback so it never touches the file log.
type InstallProgressMsg struct {
	Phase string
	Done  int64
	Total int64
}

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

	// downloads are background dependency fetches, one row each; a finished
	// row stays as a dim ✓
	downloads []InstallProgressMsg

	// cur is the stage key of the running phase, so a stream line's counts
	// drive that phase's own bar — the stream carries no PhaseKey of its own
	cur string

	done       bool  // pipeline returned (success or fail)
	failErr    error // non-nil = pipeline failed
	depsErr    error // non-nil = a dependency download failed; see DepsErr
	cancelling bool  // ctrl+c pressed, waiting for the pipeline to unwind
	finished   bool  // succeeded; waiting to switch into the review
	loading    bool  // "Opening review…" — waiting on the prefetch below
	reviewErr  error // building the review screen failed

	// reviewModel/reviewFetching prefetch the review screen once the vfs phase
	// flushes
	reviewModel    Tab
	reviewFetching bool
}

// DepsFailure reports a dependency-download failure, if that ended the run.
func (m ScanModel) DepsFailure() error { return m.depsErr }

// Cancelled reports whether the user's ctrl+c ended the screen.
func (m ScanModel) Cancelled() bool { return m.cancelling }

// Running reports that the pipeline hasn't returned yet.
func (m ScanModel) Running() bool { return !m.done }

// Busy is the container's word for Running: a pipeline in flight must not be
// interrupted, replaced, or have its settings retargeted under it.
func (m ScanModel) Busy() bool { return m.Running() }

// Failed reports that the pipeline returned an error; this screen is the only
// place it is shown, so don't discard it unread.
func (m ScanModel) Failed() bool { return m.failErr != nil }

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
	case InstallProgressMsg:
		for i, d := range m.downloads {
			if d.Phase == msg.Phase {
				m.downloads[i] = msg
				return m, nil
			}
		}
		m.downloads = append(m.downloads, msg)
		return m, nil
	case reviewReadyMsg:
		m.reviewFetching = false
		if msg.err != nil {
			m.reviewErr = msg.err
			m.loading = false
			if m.cancelling {
				return m, Left(Leave{Quit: true})
			}
			return m, nil
		}
		m.reviewModel = msg.model
		if m.loading { // "y" already pressed, waiting on this
			m.loading = false
			return m, Switch(m.reviewModel)
		}
		return m, nil
	case scanDoneMsg:
		m.done = true
		if msg.err != nil {
			if de, ok := errors.AsType[*DepsErr](msg.err); ok {
				m.depsErr = de.Err
				return m, Left(Leave{Quit: true, Err: de.Err})
			}
			m.failErr = msg.err
			m.sl.FinishRemaining(true, "")
			if m.cancelling {
				return m, Left(Leave{Quit: true})
			}
			return m, nil
		}
		m.sl.FinishRemaining(false, "done")
		m.finished = true
		// straight into review, or wait for the prefetch
		if m.cfg.ReviewNext != nil && m.reviewErr == nil {
			if m.reviewModel != nil {
				return m, Switch(m.reviewModel)
			}
			m.loading = true
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
		if m.done {
			return m, Left(Leave{Quit: true})
		}
		// second press quits even if the pipeline won't unwind
		if m.cancelling {
			return m, Left(Leave{Quit: true})
		}
		m.cancelling = true
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
				m.reviewFetching = true
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
		// "Waiting for …" lines go on the stalled stage's own row
		if m.cur != "" && strings.HasPrefix(e.Message, "Waiting for ") {
			m.sl.SetLabel(m.cur, e.Message)
			return m, nil
		}
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
	top := Banner("scan") + "\n" + m.viewDownloads() + m.viewNotes() + "\n"
	footer := m.footer()

	// The running stage's file tail gets every terminal row the chrome doesn't
	// use, so a tall window shows a long live stream instead of dead space.
	used := lipgloss.Height(top) + m.sl.HeaderLines() + lipgloss.Height(footer) + 2
	body := top + m.sl.View(m.w, max(m.h-used, 3))
	return Screen(body, footer, m.h)
}

// downloadLabel names a dependency phase for humans; the phase keys come from
// App.progressFor.
var downloadLabel = map[string]string{
	"exiftool": "exiftool",
	"location": "Location database",
}

// viewDownloads renders one row per background dependency download.
func (m ScanModel) viewDownloads() string {
	var b strings.Builder
	for _, d := range m.downloads {
		label := downloadLabel[d.Phase]
		if label == "" {
			label = d.Phase
		}
		var left string
		if d.Total > 0 && d.Done >= d.Total {
			left = " " + OK.Render("✓ ") + DimText.Render(label+" · done")
		} else {
			pct := 0.0
			if d.Total > 0 {
				pct = float64(d.Done) / float64(d.Total)
			}
			left = FaintTxt.Render(" ⬇ ") + DimText.Render(label) + "  " +
				DimText.Render(fmt.Sprintf("%s / %s  %3.0f%%", humanBytes(d.Done), humanBytes(d.Total), pct*100))
		}
		b.WriteString(row(left, "", m.w))
		b.WriteString("\n")
	}
	return b.String()
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
	case m.failErr != nil:
		b.WriteString(Bad.Render("Scan failed: "))
		b.WriteString(Text.Render(m.failErr.Error()))
		b.WriteString("\n")
		b.WriteString(Footer(KeyHint("ctrl+c", "quit"), m.w))
	case m.reviewErr != nil:
		b.WriteString(Bad.Render("Could not open review: "))
		b.WriteString(Text.Render(m.reviewErr.Error()))
		b.WriteString("\n")
		b.WriteString(Footer(KeyHint("ctrl+c", "quit"), m.w))
	case m.loading:
		b.WriteString(OK.Render("✓ Scan complete."))
		b.WriteString("  ")
		b.WriteString(DimText.Render("Opening review…"))
	case m.finished:
		// Only reachable with no ReviewNext wired — every real caller has one,
		// so this is the finished screen sitting with nothing to switch into.
		b.WriteString(OK.Render("✓ Scan complete."))
		b.WriteString("\n")
		b.WriteString(Footer(KeyHint("ctrl+c", "quit"), m.w))
	case m.cancelling:
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
