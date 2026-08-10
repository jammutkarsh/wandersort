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

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/tui"
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

	// [R] re-proposes every unsaved slice from the caller's current settings —
	// the same reset the per-segment screen offers, but library-wide, so a
	// settings change is one question here instead of one per slice. Raised
	// automatically when SettingsChangedMsg arrives while this list is on
	// screen — without this, the picker sat there showing a stale plan and the
	// only way to notice was opening a segment and seeing its own prompt.
	askRebuild      bool
	rebuildChoice   bool
	askedBySettings bool
	rebuilding      bool

	spin spinner.Model
	w, h int
}

// segmentOpenedMsg carries the segment review built off the UI goroutine.
type segmentOpenedMsg struct {
	model tea.Model
	err   error
}

// libraryRebuiltMsg carries [R]'s re-proposal result back. The picker only
// needs the side effect (vfs.Propose ran) — the tree o.Rebuild hands back is
// for a single segment screen, not this list.
type libraryRebuiltMsg struct{ err error }

func newPicker(ctx context.Context, o Options, segs []vfs.Segment) pickerModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(tui.Primary)
	return pickerModel{ctx: ctx, o: o, segs: segs, spin: sp}
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case spinner.TickMsg:
		if !m.opening && !m.rebuilding {
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
	case SettingsChangedMsg:
		m.raiseRebuildAsk(true)
		return m, nil
	case libraryRebuiltMsg:
		return m.rebuilt(msg), nil
	case tea.KeyMsg:
		if m.askRebuild && msg.String() != "ctrl+c" {
			return m.answerRebuildAsk(msg)
		}
		return m.handleKey(msg)
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
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.segs)-1 {
			m.cursor++
		}
	case "enter":
		if m.opening || m.rebuilding {
			break
		}
		m.opening = true
		m.status, m.statusErr = "", false
		return m, tea.Batch(m.open(), m.spin.Tick)
	case "r":
		return m.reopen()
	case "R":
		if !m.rebuilding {
			m.raiseRebuildAsk(false) // the reviewer asked, nothing moved under them
		}
	}
	return m, nil
}

// raiseRebuildAsk puts the reset question up, same wording rule as the
// per-segment screen's: settingsMoved only picks which text explains it.
func (m *pickerModel) raiseRebuildAsk(settingsMoved bool) {
	if m.o.Rebuild == nil { // the host can't re-propose; asking would go nowhere
		return
	}
	m.askRebuild = true
	m.rebuildChoice = true
	m.askedBySettings = settingsMoved
}

func (m pickerModel) answerRebuildAsk(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "left", "h":
		m.rebuildChoice = true
	case "right", "l":
		m.rebuildChoice = false
	case "y":
		return m.startRebuild()
	case "n", "esc":
		m.askRebuild = false
	case "enter":
		if m.rebuildChoice {
			return m.startRebuild()
		}
		m.askRebuild = false
	}
	return m, nil
}

// startRebuild re-proposes the whole library off the UI goroutine, behind the
// same spinner opening a slice uses. seg is nil: re-proposing is always
// library-wide, and the picker has no single tree to scope a result to.
func (m pickerModel) startRebuild() (tea.Model, tea.Cmd) {
	m.askRebuild = false
	m.rebuilding = true
	m.status, m.statusErr = "", false
	rebuild, ctx := m.o.Rebuild, m.ctx
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		_, err := rebuild(ctx, nil)
		return libraryRebuiltMsg{err: err}
	})
}

// rebuilt re-reads the segment list once the re-proposal lands, so counts on
// screen reflect what just got rebuilt.
func (m pickerModel) rebuilt(msg libraryRebuiltMsg) pickerModel {
	m.rebuilding = false
	if msg.err != nil {
		m.status, m.statusErr = "reset failed: "+msg.err.Error(), true
		return m
	}
	segs, err := vfs.Segments(m.ctx, m.o.DB, m.o.SegmentMonths)
	if err != nil {
		m.status, m.statusErr = err.Error(), true
		return m
	}
	if segs != nil { // nil only if the proposal itself went away under us
		m.segs = segs
	}
	m.cursor = min(m.cursor, len(m.segs)-1)
	m.status, m.statusErr = "folders re-proposed with your current settings", false
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
	if m.askRebuild {
		return m.rebuildAskView()
	}

	var b []string
	b = append(b, tui.Banner("review"))

	left := "Pick a time slice — each one is reviewed and saved on its own."
	if m.unsaved() == 0 {
		left = "Every time slice is saved. [r] discards one, [esc] exits review."
	}
	b = append(b, tui.Row(tui.DimText.Render(left),
		tui.FaintTxt.Render(fmt.Sprintf("%d time slices", len(m.segs))), m.w))

	for i, s := range m.segs {
		state := fmt.Sprintf("%d to review", s.Proposed)
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
	case m.rebuilding:
		foot = append(foot, m.spin.View()+tui.DimText.Render(" Re-proposing folders with your current settings…"))
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
		tui.KeyHint("r", "discard changes for this slice"),
	}
	if m.o.Rebuild != nil {
		hints = append(hints, tui.KeyHint("R", "reset plan"))
	}
	hints = append(hints, tui.KeyHint("esc", "leave"), tui.KeyHint("ctrl+c", "quit"))
	foot = append(foot, tui.Footer(strings.Join(hints, "   "), m.w))

	return tui.Screen(strings.Join(b, "\n"), strings.Join(foot, "\n"), m.h)
}

// rebuildAskView is the same full-screen yes/no dialog the per-segment
// screen's [R] raises, scaled to the whole library: a settings change or a
// deliberate reset invalidates every unsaved slice, not just the one open.
func (m pickerModel) rebuildAskView() string {
	choice := m.rebuildChoice
	c := tui.NewConfirmModel(m.rebuildAskTitle(), m.rebuildAskText(), &choice)
	sized, _ := c.Update(tea.WindowSizeMsg{Width: m.w, Height: m.h})
	return sized.View()
}

func (m pickerModel) rebuildAskTitle() string {
	if m.askedBySettings {
		return "Settings changed since this plan was proposed"
	}
	return "Reset this plan?"
}

func (m pickerModel) rebuildAskText() string {
	text := "Reset throws every unsaved time slice away and proposes its folders again from your current settings.\n"
	if m.askedBySettings {
		text = "Your folder rules or saved places changed, so this plan no longer matches them.\n" +
			"Reset proposes every folder you haven't saved yet again, from the new settings.\n"
	}
	text += "No keeps the plan as it is — [R] asks again.\nAlready-saved time slices are not affected."
	return text
}
