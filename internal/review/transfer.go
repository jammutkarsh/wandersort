// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

// transfer.go is [x]/[X]: copy or move every APPROVED file to the output
// right from the tree, without waiting for the review itself to be saved.
// Neither is scoped to a selection — a transfer acts on whatever the library
// has approved so far, review edits pending or not.

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

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

// raiseMoveAsk puts the one question a move needs up — copy never touches a
// source and so never asks, but a move deletes one once its copy verifies,
// and that is the one thing here worth a modal over.
func (m *Model) raiseMoveAsk() {
	m.askMove = true
	m.moveChoice = false // default to Cancel — this one deletes files
}

func (m Model) answerMoveAsk(key tea.KeyMsg) (tea.Model, tea.Cmd) {
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

// startTransfer checks there is room for everything not yet transferred,
// *then* approves the plan exactly as it stands on screen, then runs
// execute.Run off the UI goroutine behind the spinner. The space check runs
// first and deliberately: a refusal must change nothing, and once the plan is
// saved there is no going back — a rename doesn't touch a file's size, so
// counting PROPOSED and APPROVED rows both (vfs.PendingBytes) gives the same
// total whether it runs before or after the save. "Copy means copy
// everything": a rename made on screen but never explicitly saved still has
// to be what lands on disk, not whatever the database held from an earlier
// save, and files a transfer marks DONE can never be re-planned — so the plan
// on screen and the one that gets written must be the same plan.
func (m Model) startTransfer(mode execute.Mode) (tea.Model, tea.Cmd) {
	m.askMove = false
	needed, err := vfs.PendingBytes(m.ctx, m.db)
	if err != nil {
		m.statusMsg, m.statusIsErr = err.Error(), true
		return m, nil
	}
	if needed == 0 {
		m.statusMsg, m.statusIsErr = "nothing left to transfer", true
		return m, nil
	}
	// ponytail: a same-volume move only renames, so it needs no free space at
	// all — this still refuses one on a nearly-full disk. Known limit; split
	// the check by mode (or by volume) if that's ever the transfer someone's
	// blocked on.
	if free, err := volume.FreeBytes(m.outputDir); err == nil && uint64(needed) > free {
		m.statusMsg, m.statusIsErr = fmt.Sprintf(
			"not enough free space: needs %s, only %s free at the output",
			volume.HumanBytes(uint64(needed)), volume.HumanBytes(free)), true
		return m, nil
	}
	if err := vfs.Confirm(m.ctx, m.db, m.tree); err != nil {
		m.statusMsg, m.statusIsErr = err.Error(), true
		return m, nil
	}

	m.transferring = true
	m.transferMode = mode
	m.prog, m.bytes = transferProgressMsg{}, 0
	m.statusMsg, m.statusIsErr = "", false
	// Buffered and lossy: a progress report is worth only what the next frame
	// draws, so a full channel drops one rather than pacing the transfer to
	// the terminal.
	m.progCh = make(chan transferProgressMsg, 1)
	ch := m.progCh
	ctx, db, outputDir, log := m.ctx, m.db, m.outputDir, m.log
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

// transferred reports what the transfer did, then reloads the tree — on
// every path, including a failed run. `Confirm` already saved the plan in
// startTransfer before execute.Run ever started, so the database's rows (and
// their names) are the current plan regardless of whether the run itself
// succeeded; leaving the old in-memory tree on screen after a save is what
// makes a later edit's node IDs stop matching what Confirm can find (a
// reported bug — "invalid review tree: unknown node id"). The reload goes
// through the same resetCmd [R] uses (postTransferSync tells reset not to
// relabel it a discard).
func (m Model) transferred(msg transferredMsg) (Model, tea.Cmd) {
	m.transferring = false
	// Accumulated regardless of msg.err: a hard failure (e.g. the database
	// backup itself) means rep is the zero value, so this stays a no-op, but
	// a run that copied some files before failing on a later one must still
	// count what it actually got done.
	m.transferDone += msg.rep.Done
	m.transferFailed += msg.rep.Failed
	verb := "Copied"
	if msg.mode == execute.ModeMove {
		verb = "Moved"
	}
	switch {
	case msg.err != nil:
		m.statusMsg, m.statusIsErr = msg.err.Error(), true
	case msg.rep.Failed > 0:
		m.statusMsg, m.statusIsErr = fmt.Sprintf("%s %d files, %d failed — see the log", verb, msg.rep.Done, msg.rep.Failed), true
	default:
		m.statusMsg, m.statusIsErr = fmt.Sprintf("%s %d files to the output", verb, msg.rep.Done), false
	}
	m.postTransferSync = true
	return m, resetCmd(m.ctx, m.db)
}

// transferRow is the copy/move progress bar, in the same shape the config
// wizard's download row uses: a bar plus what it is counting. Until the first
// report lands there is no total to size a bar with (execute is still loading
// its rows), so it starts as the spinner alone.
func (m Model) transferRow() string {
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
	return tui.Row(left, tui.FaintTxt.Render(volume.HumanBytes(uint64(m.bytes))), m.width)
}

// moveAskView is [X]'s one question — the only destructive act this screen
// can trigger outside a save. Default lands on Cancel (moveChoice starts
// false): a move deletes files on disk once their copies verify.
func (m Model) moveAskView() string {
	choice := m.moveChoice
	c := tui.NewConfirmModel(
		"Move files instead of copying?",
		"Each source file is deleted once its copy at the output is verified complete — this cannot be undone.\n"+
			"No copies instead, which never touches a source — [x] does the same without asking.",
		&choice,
	)
	sized, _ := c.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	return sized.View()
}
