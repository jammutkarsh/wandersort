// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

// segments.go is the screen a segmented review opens on: a list of time
// slices, each opened, reviewed and saved on its own. A 400-folder library is
// otherwise one all-or-nothing decision.

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// pickerModel lists the proposal's segments. Opening one builds its tree in a
// fresh query rather than filtering the whole tree: a segment is a range over
// taken_at, which is exactly what the database is good at.
type pickerModel struct {
	ctx  context.Context
	o    Options
	segs []vfs.Segment

	cursor  int
	opening bool // a segment's tree is being built off the UI goroutine
	// saved counts the segments confirmed since this review started — what the
	// caller reports as the outcome.
	saved      int
	quitWarned bool
	status     string
	statusErr  bool

	// [x] copies every APPROVED row to the output now, no question asked —
	// copy never touches a source, so there is nothing to warn about. [X]
	// moves instead, which deletes each source once its copy verifies, and
	// asks first — the one destructive act this screen can trigger. Neither
	// is scoped to the selected slice — a transfer acts on whatever the
	// library has approved so far, segmented review or not, so a reviewer can
	// copy as they go instead of waiting for every slice to be signed off.
	askMove      bool
	moveChoice   bool // which button the modal has under the cursor
	transferring bool
	transferMode execute.Mode // which one is running, for the spinner label
	// prog is the last progress report drawn, progCh the channel execute's
	// OnProgress feeds it down. A transfer is the one thing here that runs for
	// minutes over gigabytes, so it gets a bar rather than a bare spinner.
	prog   transferProgressMsg
	progCh chan transferProgressMsg
	bytes  int64 // running total, since OnProgress reports one file's size

	spin spinner.Model
	bar  progress.Model
	w, h int
}

// segmentOpenedMsg carries the segment review built off the UI goroutine.
type segmentOpenedMsg struct {
	model tea.Model
	err   error
}

// transferredMsg carries [x]/[X]'s execute.Run result back.
type transferredMsg struct {
	mode execute.Mode
	rep  execute.Report
	err  error
}

// transferProgressMsg is one transferred file, as execute.OnProgress reported
// it. A background goroutine cannot push a message into bubbletea, so each one
// re-arms the command that read it off the channel.
type transferProgressMsg struct {
	target      string
	bytes       int64
	done, total int
}

// waitProgress reads the next report. A closed channel means the run is over
// and transferredMsg is already on its way — nothing left to say.
func waitProgress(ch chan transferProgressMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func newPicker(ctx context.Context, o Options, segs []vfs.Segment) pickerModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(tui.Primary)
	return pickerModel{
		ctx: ctx, o: o, segs: segs, spin: sp,
		bar: progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage()),
	}
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case spinner.TickMsg:
		if !m.opening && !m.transferring {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case segmentOpenedMsg:
		m.opening = false
		if msg.err != nil {
			m.status, m.statusErr = msg.err.Error(), true
			return m, nil
		}
		return m, tui.Switch(msg.model)
	case transferProgressMsg:
		if !m.transferring { // the result already landed — a late report draws nothing
			return m, nil
		}
		m.prog = msg
		m.bytes += msg.bytes
		return m, waitProgress(m.progCh)
	case transferredMsg:
		return m.transferred(msg), nil
	case tea.KeyMsg:
		switch {
		case m.askMove && msg.String() != "ctrl+c":
			return m.answerMoveAsk(msg)
		default:
			return m.handleKey(msg)
		}
	}
	return m, nil
}

func (m pickerModel) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.quitWarned = m.quitWarned && (key.String() == "esc" || key.String() == "ctrl+c")
	switch key.String() {
	case "esc", "ctrl+c":
		// ctrl+c is the app's quit key everywhere, so it goes straight out;
		// [esc] warns once while slices are still unreviewed.
		if left := m.unsaved(); left > 0 && key.String() == "esc" && !m.quitWarned {
			m.quitWarned = true
			m.status = fmt.Sprintf("%d time slices not saved yet — press esc again to leave them", left)
			m.statusErr = true
			return m, nil
		}
		return m, tui.Switch(nil)
	case "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down":
		if m.cursor < len(m.segs)-1 {
			m.cursor++
		}
	case "enter":
		if m.opening {
			break
		}
		m.opening = true
		m.status, m.statusErr = "", false
		return m, tea.Batch(m.open(), m.spin.Tick)
	case "A":
		return m.acceptAsProposed()
	case "ctrl+x":
		return m.reopen()
	case "x":
		if !m.transferring {
			return m.startTransfer(execute.ModeCopy)
		}
	case "X":
		if !m.transferring {
			m.raiseMoveAsk()
		}
	}
	return m, nil
}

// raiseMoveAsk puts the one question a move needs up — copy never touches a
// source and so never asks, but a move deletes one once its copy verifies,
// and that is the one thing here worth a modal over.
func (m *pickerModel) raiseMoveAsk() {
	m.askMove = true
	m.moveChoice = false // default to Cancel, unlike the rebuild ask — this one deletes files
}

func (m pickerModel) answerMoveAsk(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "left":
		m.moveChoice = true
	case "right":
		m.moveChoice = false
	case "y":
		return m.startTransfer(execute.ModeMove)
	case "n", "esc":
		m.askMove = false
	case "enter":
		if m.moveChoice {
			return m.startTransfer(execute.ModeMove)
		}
		m.askMove = false
	}
	return m, nil
}

// startTransfer runs execute.Run off the UI goroutine behind the same
// spinner opening a slice and rebuilding both use. Acts on the whole
// library's APPROVED rows, not just the selected slice — see the field
// comment on askMove.
func (m pickerModel) startTransfer(mode execute.Mode) (tea.Model, tea.Cmd) {
	m.askMove = false
	m.transferring = true
	m.transferMode = mode
	m.prog, m.bytes = transferProgressMsg{}, 0
	m.status, m.statusErr = "", false
	// Buffered and lossy: a progress report is worth only what the next frame
	// draws, so a full channel drops one rather than pacing the transfer to
	// the terminal.
	m.progCh = make(chan transferProgressMsg, 1)
	ch := m.progCh
	ctx, db, outputDir, log := m.ctx, m.o.DB, m.o.OutputDir, m.o.Log
	// run before waitProgress in the batch: the only thing that ever closes ch
	// is the run finishing.
	run := func() tea.Msg {
		rep, err := execute.Run(ctx, db, log, outputDir, execute.Options{
			Mode: mode,
			OnProgress: func(target string, bytes int64, done, total int) {
				select {
				case ch <- transferProgressMsg{target: target, bytes: bytes, done: done, total: total}:
				default:
				}
			},
		})
		close(ch)
		return transferredMsg{mode: mode, rep: rep, err: err}
	}
	return m, tea.Batch(m.spin.Tick, run, waitProgress(ch))
}

// transferred reports what the transfer did. Approved rows a transfer just
// marked DONE don't change what the picker's own counts mean — Proposed and
// Approved are still exactly what BuildTree would show — so there is nothing
// to re-read here, only a status line to report.
func (m pickerModel) transferred(msg transferredMsg) pickerModel {
	m.transferring = false
	verb := "Copied"
	if msg.mode == execute.ModeMove {
		verb = "Moved"
	}
	switch {
	case msg.err != nil:
		m.status, m.statusErr = msg.err.Error(), true
	case msg.rep.Done == 0 && msg.rep.Failed == 0:
		m.status, m.statusErr = "nothing approved yet to transfer", true
	case msg.rep.Failed > 0:
		m.status, m.statusErr = fmt.Sprintf("%s %d files, %d failed — see the log", verb, msg.rep.Done, msg.rep.Failed), true
	default:
		m.status, m.statusErr = fmt.Sprintf("%s %d files to the output", verb, msg.rep.Done), false
	}
	return m
}

// open builds the selected segment's tree and its review screen off the UI
// goroutine. The screen finalizes in-program (vfs.Confirm scoped to this
// segment) and hands back here, so the reviewer saves one slice and carries on.
func (m pickerModel) open() tea.Cmd {
	seg := m.segs[m.cursor]
	ctx, o := m.ctx, m.o
	// A snapshot: where the segment screen returns to, saved or not. It is taken
	// mid-open, so the spinner state has to be cleared — coming back to a list
	// that still says "Opening…" reads as a hang.
	host := m
	host.opening = false
	return func() tea.Msg {
		tree, err := vfs.BuildTree(ctx, o.DB, &seg)
		if err != nil {
			return segmentOpenedMsg{err: err}
		}
		if len(tree) == 0 {
			return segmentOpenedMsg{err: fmt.Errorf("%s has nothing left to review", seg.Label)}
		}
		o.Tree, o.Segment = tree, &seg
		return segmentOpenedMsg{model: newSegmentScreen(ctx, o, &host)}
	}
}

// reenter is what a saved segment returns to: this same list with the counts
// re-read, one more segment saved.
func (m pickerModel) reenter() (tea.Model, error) {
	segs, err := vfs.Segments(m.ctx, m.o.DB, m.o.SegmentMonths)
	if err != nil {
		return nil, err
	}
	m.saved++
	if segs != nil { // nil only if the proposal itself went away under us
		m.segs = segs
	}
	m.cursor = min(m.cursor, len(m.segs)-1)
	m.opening, m.quitWarned = false, false
	m.status, m.statusErr = "", false
	return m, nil
}

// acceptAsProposed signs the slice off without opening it — the answer to "the
// folders here are fine as they are". Opening a slice and pressing [esc] does
// not save any more when nothing was edited (that is just a look around), so
// this is where an untouched slice gets approved.
func (m pickerModel) acceptAsProposed() (tea.Model, tea.Cmd) {
	seg := m.segs[m.cursor]
	if seg.Proposed == 0 {
		m.status, m.statusErr = seg.Label+" is already saved", true
		return m, nil
	}
	if err := vfs.ApproveSegment(m.ctx, m.o.DB, &seg); err != nil {
		m.status, m.statusErr = err.Error(), true
		return m, nil
	}
	next, err := m.reenter()
	if err != nil {
		m.status, m.statusErr = err.Error(), true
		return m, nil
	}
	p := next.(pickerModel)
	p.status, p.statusErr = seg.Label+" saved as proposed", false
	return p, nil
}

// reopen discards a saved slice's approval and puts it back to reviewable —
// the only way back into one that was signed off, short of rebuilding the
// whole proposal. It does not re-propose anything: the slice's folders stay
// exactly as they were, just no longer marked saved.
func (m pickerModel) reopen() (tea.Model, tea.Cmd) {
	seg := m.segs[m.cursor]
	if seg.Approved == 0 {
		m.status, m.statusErr = seg.Label+" isn't saved yet — [enter] reviews it", true
		return m, nil
	}
	if err := vfs.ReopenSegment(m.ctx, m.o.DB, &seg); err != nil {
		m.status, m.statusErr = err.Error(), true
		return m, nil
	}
	segs, err := vfs.Segments(m.ctx, m.o.DB, m.o.SegmentMonths)
	if err != nil {
		m.status, m.statusErr = err.Error(), true
		return m, nil
	}
	if segs != nil {
		m.segs = segs
	}
	m.status, m.statusErr = seg.Label+" is open for review again", false
	return m, nil
}

// unsaved is how many segments still hold entries nobody has approved.
func (m pickerModel) unsaved() int {
	n := 0
	for _, s := range m.segs {
		if s.Proposed > 0 {
			n++
		}
	}
	return n
}

func (m pickerModel) View() string {
	if m.askMove {
		return m.moveAskView()
	}

	var b []string
	b = append(b, tui.Banner("review"))

	left := "Pick a time slice — each one is reviewed and saved on its own."
	if m.unsaved() == 0 {
		left = "Every time slice is saved. [ctrl+x] discards one, [esc] exits review."
	}
	b = append(b, tui.Row(tui.DimText.Render(left),
		tui.FaintTxt.Render(fmt.Sprintf("%d time slices", len(m.segs))), m.w))

	for i, s := range m.segs {
		// the right column is folders, not a second file count: a review is a
		// decision about folders, and the files are already named on the left
		state := fmt.Sprintf("%d folders", s.Folders)
		styled := tui.FaintTxt.Render(state)
		if s.Proposed == 0 {
			state, styled = "✓ saved", tui.OK.Render("✓ saved")
		}
		row := fmt.Sprintf("%-16s %d files", s.Label, s.Proposed+s.Approved)
		if i == m.cursor {
			// plain inside the highlight: a nested ANSI reset would cut the
			// background short part-way through the line
			b = append(b, tui.Selected.Render(tui.Row("❯ "+row, state, m.w)))
			continue
		}
		b = append(b, tui.Row("  "+tui.Text.Render(row), styled, m.w))
	}

	var foot []string
	switch {
	case m.opening:
		foot = append(foot, m.spin.View()+tui.DimText.Render(" Opening…"))
	case m.transferring:
		foot = append(foot, m.transferRow())
	}
	if m.status != "" {
		if m.statusErr {
			foot = append(foot, tui.Attn.Render("⚠ "+m.status))
		} else {
			foot = append(foot, tui.DimText.Render(m.status))
		}
	}
	hints := []string{
		tui.KeyHint("↑↓", "move"),
		tui.KeyHint("enter", "review this slice"),
		tui.KeyHint("A", "accept this slice as proposed"),
		tui.KeyHint("ctrl+x", "discard changes for this slice"),
	}
	hints = append(hints, tui.KeyHint("x", "copy approved files now"), tui.KeyHint("X", "move approved files now"))
	hints = append(hints, tui.KeyHint("esc", "leave"), tui.KeyHint("ctrl+c", "quit"))
	foot = append(foot, tui.Footer(strings.Join(hints, "   "), m.w))

	return tui.Screen(strings.Join(b, "\n"), strings.Join(foot, "\n"), m.h)
}

// transferRow is the copy/move progress bar, in the same shape the config
// wizard's download row uses: a bar plus what it is counting. Until the first
// report lands there is no total to size a bar with (execute is still loading
// its rows), so it starts as the spinner alone.
func (m pickerModel) transferRow() string {
	verb := "Copying"
	if m.transferMode == execute.ModeMove {
		verb = "Moving"
	}
	if m.prog.total == 0 {
		return m.spin.View() + tui.DimText.Render(" "+verb+" approved files to the output…")
	}
	pct := float64(m.prog.done) / float64(m.prog.total)
	left := m.spin.View() + " " + tui.DimText.Render(verb) + "  " + m.bar.ViewAs(pct) + "  " +
		tui.DimText.Render(fmt.Sprintf("%d/%d", m.prog.done, m.prog.total))
	return tui.Row(left, tui.FaintTxt.Render(volume.HumanBytes(uint64(m.bytes))), m.w)
}

// moveAskView is [X]'s one question — the only destructive act this screen
// can trigger. Default lands on Cancel (moveChoice starts false): a move
// deletes files on disk once their copies verify.
func (m pickerModel) moveAskView() string {
	choice := m.moveChoice
	c := tui.NewConfirmModel(
		"Move files instead of copying?",
		"Each source file is deleted once its copy at the output is verified complete — this cannot be undone.\n"+
			"No copies instead, which never touches a source — [x] does the same without asking.",
		&choice,
	)
	sized, _ := c.Update(tea.WindowSizeMsg{Width: m.w, Height: m.h})
	return sized.View()
}
