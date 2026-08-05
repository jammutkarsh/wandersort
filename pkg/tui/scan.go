// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

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

// DepsErr marks a Pipeline failure that happened before any phase started —
// downloading a dependency failed. There's no phase progress on screen worth
// leaving up for this, and the raw error (a URL, a transport failure) reads
// better as a plain line in the normal terminal than crammed into the
// footer, so the scan screen quits immediately on it instead of rendering it
// like an ordinary phase failure; the caller prints it once back outside the
// TUI (see DepsFailure).
type DepsErr struct{ Err error }

func (e *DepsErr) Error() string { return e.Err.Error() }
func (e *DepsErr) Unwrap() error { return e.Err }

// LogEventMsg carries one logger.Event into the TUI. The scan command forwards
// events from the TUI logger's sink into the program via program.Send.
type LogEventMsg struct{ Event logger.Event }

// InstallProgressMsg carries dependency-download byte progress into the scan
// screen. It comes straight from a callback (not the logger), so the per-byte
// ticks never touch the file log. The downloads run in the background while
// the pipeline works (workflow.Deps) — this is their only visibility.
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
	model tea.Model
	err   error
}

// ScanConfig wires the scan screen to the pipeline.
type ScanConfig struct {
	// Pipeline runs the scan (RunScan) and blocks until it finishes. Its
	// user-facing/stream log lines must reach the screen via LogEventMsg.
	Pipeline func() error
	// Cancel cancels the pipeline context on ctrl+c.
	Cancel context.CancelFunc
	// ReviewNext builds the review screen for the post-scan "continue?" prompt.
	// nil (or an error) means no in-program review — the screen just exits.
	ReviewNext func() (tea.Model, error)
}

// ScanModel is the full-screen live scan view: a Docker-buildkit-style stack
// of stage rows (StageList) with the files being processed streaming under the
// running stage, milestone notes under the banner, and warnings pinned above
// the footer.
type ScanModel struct {
	cfg      ScanConfig
	sl       StageList
	notes    []string
	warnings []string
	w, h     int

	// downloads are the background dependency fetches, one row each under the
	// notes: a byte count while running, a dim ✓ once complete (a row that
	// vanishes the moment it fills reads as a failure). Dependencies already on
	// disk never report, so nothing renders for them.
	downloads []InstallProgressMsg

	// cur is the stage key of the running phase, so a stream line's counts
	// drive that phase's own bar — the stream carries no PhaseKey of its own
	cur string

	done       bool  // pipeline returned (success or fail)
	failErr    error // non-nil = pipeline failed
	depsErr    error // non-nil = a dependency download failed; see DepsErr
	cancelling bool  // ctrl+c pressed, waiting for the pipeline to unwind
	finished   bool  // succeeded; showing the review prompt
	loading    bool  // "y" pressed before the prefetch below landed
	reviewErr  error // building the review screen failed
	gotoReview bool  // user chose review and there was no in-program ReviewNext

	// reviewModel/reviewFetching prefetch the review screen (vfs.BuildTree)
	// as soon as the vfs phase flushes, ahead of "y", so it's usually ready
	// by the time the reviewer reacts. A "n" quit mid-fetch discards nothing.
	reviewModel    tea.Model
	reviewFetching bool
}

// WantsReview reports whether the user accepted the post-scan review prompt but
// no in-program review screen was wired (ReviewNext==nil) — the caller then
// launches review itself after the program exits.
func (m ScanModel) WantsReview() bool { return m.gotoReview }

// DepsFailure reports a dependency-download failure (see DepsErr), if that's
// why the pipeline ended — the caller prints it in the normal terminal once
// the TUI has exited, rather than the screen showing it in its own footer.
func (m ScanModel) DepsFailure() error { return m.depsErr }

// Cancelled reports whether the user's own ctrl+c is why the screen ended —
// true once ctrl+c has been pressed on a still-running pipeline, regardless
// of which stage was in flight (the pipeline itself, or the review prefetch
// that follows it) or which error field the cancellation surfaced through.
// The caller prints a short "cancelled" line in the normal terminal once the
// TUI has exited — the reviewer asked to leave, so the screen quits straight
// away instead of parking them on a failure footer they'd have to press q on.
func (m ScanModel) Cancelled() bool { return m.cancelling }

// NewScanModel builds the scan screen. The five stages mirror the workflow
// phases (keys match logger.PhaseKey: scan/hash/exif/score/vfs).
func NewScanModel(cfg ScanConfig) ScanModel {
	sl := NewStageList(
		nil,
		&Stage{Key: "scan", Name: "Scan"},
		&Stage{Key: "hash", Name: "Hash", HasBar: true},
		&Stage{Key: "exif", Name: "Metadata", HasBar: true},
		&Stage{Key: "score", Name: "Score"},
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
				return m, tea.Quit
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
				return m, tea.Quit
			}
			m.failErr = msg.err
			m.sl.FinishRemaining(true, "")
			if m.cancelling {
				return m, tea.Quit
			}
			return m, nil
		}
		m.sl.FinishRemaining(false, "done")
		m.finished = true
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
			return m, tea.Quit
		}
		m.cancelling = true
		return m, nil
	case "q":
		if m.done {
			return m, tea.Quit
		}
	}
	if m.finished && !m.loading {
		switch k.String() {
		case "y", "Y", "enter":
			switch {
			case m.cfg.ReviewNext == nil:
				m.gotoReview = true
				return m, tea.Quit
			case m.reviewModel != nil: // prefetch already landed — instant switch
				return m, Switch(m.reviewModel)
			case m.reviewErr != nil: // prefetch already failed — footer shows it
				return m, nil
			case m.reviewFetching: // still in flight — switch once it lands
				m.loading = true
				return m, nil
			default: // shouldn't happen (vfs "done" always precedes "finished"); fall back
				m.loading = true
				return m, m.fetchReview()
			}
		case "n", "N":
			return m, tea.Quit
		}
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
	// Phase transition — route to the matching stage row. The done message
	// carries its own elapsed (logger.ElapsedKey); strip the duplicated
	// " in <elapsed>" suffix so the time renders once, in the right column.
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

	// Stream — per-file feed line; the hash and exif phases also carry the
	// running count, so the bar advances one file at a time instead of in
	// throttled jumps.
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
		// install.Coordinator's "Waiting for the … download to finish…" lines
		// (the only UserKey lines worded this way) belong on the stalled
		// stage's own row, not buried in the notes scroll — a running stage
		// with no bar yet (still parked on the download) otherwise looks
		// hung instead of saying why.
		if m.cur != "" && strings.HasPrefix(e.Message, "Waiting for ") {
			m.sl.SetLabel(m.cur, e.Message)
			return m, nil
		}
		m.notes = append(m.notes, e.Message)
	}
	return m, nil
}

// progressCmd drives the running phase's bar from any event carrying
// hashed|extracted + total counts; events without them are a no-op, as is a
// phase with no bar (SetProgress ignores an unknown key).
func (m *ScanModel) progressCmd(e logger.Event) tea.Cmd {
	total, ok := toInt(e.Attrs["total"])
	if !ok || total <= 0 {
		return nil
	}
	cur, has := toInt(e.Attrs["hashed"])
	if !has {
		cur, has = toInt(e.Attrs["extracted"])
	}
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

// viewDownloads renders one row per background dependency download, above the
// notes: label + bytes while running, ✓ done after — the same treatment the
// config wizard gives its download.
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

// maxFooterWarnings caps how many warnings render above the footer — a folder
// full of unsupported files would otherwise push the whole screen off. The
// rest are counted, and all of them are in the log file.
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
		b.WriteString(Footer(KeyHint("q", "quit"), m.w))
	case m.reviewErr != nil:
		b.WriteString(Bad.Render("Could not open review: "))
		b.WriteString(Text.Render(m.reviewErr.Error()))
		b.WriteString("\n")
		b.WriteString(Footer(KeyHint("q", "quit"), m.w))
	case m.loading:
		b.WriteString(OK.Render("✓ Scan complete."))
		b.WriteString("  ")
		b.WriteString(DimText.Render("Opening review…"))
	case m.finished:
		b.WriteString(OK.Render("✓ Scan complete."))
		b.WriteString("  ")
		b.WriteString(DimText.Render("Continue to review?"))
		b.WriteString("\n")
		b.WriteString(Footer(KeyHint("y", "review")+"   "+KeyHint("n", "quit"), m.w))
	case m.cancelling:
		b.WriteString(Attn.Render("Cancelling…"))
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
