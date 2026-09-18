// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// pickerFixture is a two-slice proposal: 2023 already saved, 2024 still to
// review — the state the picker exists to show.
func pickerFixture(t *testing.T) (pickerModel, *db.DB) {
	t.Helper()
	d := dbtest.New(t)
	seed := func(id int64, year int, status string) {
		name := fmt.Sprintf("IMG_%d.jpg", id)
		dbtest.SeedFile(t, d, id, "/src", name, 100)
		at := db.FormatTime(time.Date(year, time.March, 1, 12, 0, 0, 0, time.UTC))
		if _, err := d.ExecContext(context.Background(),
			`INSERT INTO virtual_fs_entries (file_id, source_path, target_path, status, taken_at)
			 VALUES (?, ?, ?, ?, ?)`,
			id, "/src/"+name, fmt.Sprintf("%d/03_March/%s", year, name), status, at); err != nil {
			t.Fatal(err)
		}
	}
	seed(1, 2023, db.StatusApproved)
	seed(2, 2024, db.StatusProposed)

	o := Options{DB: d, Log: logger.NewNoopLogger(), SegmentMonths: 12, OutputDir: t.TempDir()}
	segs, err := vfs.Segments(context.Background(), d, o.SegmentMonths)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 {
		t.Fatalf("segments = %d, want 2", len(segs))
	}
	return newPicker(context.Background(), o, segs), d
}

func TestPickerOpensSelectedSegment(t *testing.T) {
	m, _ := pickerFixture(t)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	next, cmd := next.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p := next.(pickerModel); p.cursor != 1 || !p.opening {
		t.Fatalf("picker = cursor %d opening %v, want 1/true", p.cursor, p.opening)
	}
	if cmd == nil {
		t.Fatal("enter dispatched no command")
	}

	// the open runs off the UI goroutine; drive it here and check what came back
	msg := drainOpen(t, cmd())
	if msg.err != nil {
		t.Fatalf("open: %v", msg.err)
	}
	s, ok := msg.model.(screen)
	if !ok {
		t.Fatalf("opened %T, want a review screen", msg.model)
	}
	if s.seg == nil || s.seg.Label != "2024" {
		t.Errorf("screen segment = %+v, want 2024", s.seg)
	}
	if s.host == nil {
		t.Error("screen has no picker to return to — a saved slice would end the review")
	}
}

// drainOpen finds the segmentOpenedMsg inside the batch [enter] returns (the
// open itself, plus the spinner tick that runs while it works).
func drainOpen(t *testing.T, msg tea.Msg) segmentOpenedMsg {
	t.Helper()
	switch m := msg.(type) {
	case segmentOpenedMsg:
		return m
	case tea.BatchMsg:
		for _, c := range m {
			if c == nil {
				continue
			}
			if om, ok := c().(segmentOpenedMsg); ok {
				return om
			}
		}
	}
	t.Fatalf("got %T, want segmentOpenedMsg", msg)
	return segmentOpenedMsg{}
}

// TestPickerQuitWarnsAboutUnsavedSlices: [esc] is not a save, and the whole
// point of segmenting is that leaving early loses the slices not yet done.
func TestPickerQuitWarnsAboutUnsavedSlices(t *testing.T) {
	m, _ := pickerFixture(t)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	p := next.(pickerModel)
	if cmd != nil {
		t.Fatal("first esc left immediately, want a warning first")
	}
	if !p.statusErr || p.status == "" {
		t.Errorf("no warning after first esc: %q", p.status)
	}

	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("second esc did not leave")
	}
	sw, ok := cmd().(tui.SwitchMsg)
	if !ok || sw.Next != nil {
		t.Fatalf("second esc sent %#v, want a Switch(nil) handing back to the host", cmd())
	}
}

func TestPickerReopenSavedSegment(t *testing.T) {
	m, d := pickerFixture(t)

	// cursor starts on 2023, the saved one
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	p := next.(pickerModel)
	if p.statusErr {
		t.Fatalf("reopen reported: %s", p.status)
	}
	if p.segs[0].Proposed != 1 || p.segs[0].Approved != 0 {
		t.Errorf("2023 counts = %d/%d, want 1 proposed / 0 approved", p.segs[0].Proposed, p.segs[0].Approved)
	}
	var status string
	if err := d.SQL.Get(&status, `SELECT status FROM virtual_fs_entries WHERE file_id = 1`); err != nil {
		t.Fatal(err)
	}
	if status != db.StatusProposed {
		t.Errorf("row status = %q, want PROPOSED", status)
	}

	// the unsaved slice has nothing to re-open — say so rather than no-op
	next, _ = p.Update(tea.KeyMsg{Type: tea.KeyDown})
	next, _ = next.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	if !next.(pickerModel).statusErr {
		t.Error("re-opening an unsaved slice said nothing")
	}
}

// TestSegmentScreenBackToPicker: leaving one slice without saving is a step
// back to the list, not the end of the review — the other slices are still
// waiting. Also checks the picker comes back without its "Opening…" spinner,
// since the snapshot it returns to was taken mid-open.
func TestSegmentScreenBackToPicker(t *testing.T) {
	m, _ := pickerFixture(t)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := next.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	s := drainOpen(t, cmd()).model.(screen)

	// nothing was edited, so [esc] asks nothing — looking around a slice is not
	// a decision. [A] on the picker is how an untouched slice gets approved.
	back, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if b := back.(screen); b.confirmed {
		t.Error("[esc] confirmed the slice")
	}
	if b := back.(screen); b.inner.askExit {
		t.Error("[esc] over an unedited tree raised the save-or-discard ask")
	}
	if cmd == nil {
		t.Fatal("[esc] went nowhere")
	}
	sw, ok := cmd().(tui.SwitchMsg)
	if !ok {
		t.Fatalf("[esc] sent %#v, want a Switch", cmd())
	}
	p, ok := sw.Next.(pickerModel)
	if !ok {
		t.Fatalf("[esc] switched to %T, want back to the picker", sw.Next)
	}
	if p.opening {
		t.Error("picker came back still opening — the spinner would never stop")
	}
}

// TestSegmentScreenEscStillAsksAfterAnEdit: the no-ask shortcut is only for a
// tree nobody touched. One edit and [esc] has a real question to put.
func TestSegmentScreenEscStillAsksAfterAnEdit(t *testing.T) {
	m, _ := pickerFixture(t)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := next.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	s := drainOpen(t, cmd()).model.(screen)
	s.inner.width, s.inner.height = 80, 24

	edited, _ := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	edited, _ = edited.(screen).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Trip")})
	edited, _ = edited.(screen).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !edited.(screen).inner.hasEdits() {
		t.Fatal("rename did not register as an edit")
	}

	asked, _ := edited.(screen).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !asked.(screen).inner.askExit {
		t.Error("[esc] after an edit did not ask to save or discard")
	}
}

// TestPickerAcceptsSliceAsProposed: [A] signs a slice off without opening it —
// the way an untouched slice gets saved, since [esc] over an unedited tree is
// just a step back now.
func TestPickerAcceptsSliceAsProposed(t *testing.T) {
	m, d := pickerFixture(t)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown}) // 2024, the unsaved one
	next, _ = next.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("A")})
	p := next.(pickerModel)
	if p.statusErr {
		t.Fatalf("accept reported: %s", p.status)
	}
	if p.saved != 1 {
		t.Errorf("saved slices = %d, want 1", p.saved)
	}
	if p.segs[1].Proposed != 0 || p.segs[1].Approved != 1 {
		t.Errorf("2024 counts = %d/%d, want 0 proposed / 1 approved", p.segs[1].Proposed, p.segs[1].Approved)
	}
	var status string
	if err := d.SQL.Get(&status, `SELECT status FROM virtual_fs_entries WHERE file_id = 2`); err != nil {
		t.Fatal(err)
	}
	if status != db.StatusApproved {
		t.Errorf("row status = %q, want APPROVED", status)
	}

	// an already-saved slice has nothing to accept — say so rather than no-op
	next, _ = p.Update(tea.KeyMsg{Type: tea.KeyUp})
	next, _ = next.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("A")})
	if !next.(pickerModel).statusErr {
		t.Error("accepting an already-saved slice said nothing")
	}
}

// TestCtrlCInsideSegmentNeverBacksToPicker: ctrl+c is the one guarantee that
// always ends the program, hosted or not — unlike [esc]'s Discard, which
// steps back to the picker. A reported bug had ctrl+c inside a segment
// screen doing the same "back to picker" as esc, which meant "ctrl+c quits
// the app" was a lie in the one place a reviewer would reach for it hardest.
func TestCtrlCInsideSegmentNeverBacksToPicker(t *testing.T) {
	m, _ := pickerFixture(t)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := next.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	s := drainOpen(t, cmd()).model.(screen)

	_, cmd = s.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c went nowhere")
	}
	sw, ok := cmd().(tui.SwitchMsg)
	if !ok {
		t.Fatalf("ctrl+c sent %#v, want a Switch", cmd())
	}
	if sw.Next != nil {
		t.Fatalf("ctrl+c switched to %T, want Switch(nil) — never back to the picker", sw.Next)
	}
}

// TestSegmentResetReloadsFromDB: [R] discards an in-memory rename and reloads
// this one slice's still-proposed rows from the database, without touching
// the other (already-saved) segment.
func TestSegmentResetReloadsFromDB(t *testing.T) {
	m, _ := pickerFixture(t)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := next.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	s := drainOpen(t, cmd()).model.(screen)

	renamed := s.inner
	renamed.applyRename("Renamed")
	if renamed.rows[0].node.Name != "Renamed" || !renamed.hasEdits() {
		t.Fatal("setup: rename should have landed and left an undo step")
	}

	next2, cmd := renamed.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if cmd == nil {
		t.Fatal("[R] dispatched nothing")
	}
	if !next2.(Model).resetting {
		t.Fatal("[R] should mark the screen as resetting until the reload lands")
	}
	msg := drainReset(t, cmd())
	if msg.err != nil {
		t.Fatal(msg.err)
	}

	got := next2.(Model).reset(msg)
	if got.resetting {
		t.Error("still resetting after the tree landed")
	}
	if got.hasEdits() {
		t.Error("reset must clear the undo stack")
	}
	if len(got.tree) != 1 || got.tree[0].Name != "2024" {
		t.Errorf("reset tree = %+v, want the un-renamed 2024 slice", got.tree)
	}
}

func drainReset(t *testing.T, msg tea.Msg) resetMsg {
	t.Helper()
	switch m := msg.(type) {
	case resetMsg:
		return m
	case tea.BatchMsg:
		for _, c := range m {
			if c == nil {
				continue
			}
			if rm, ok := c().(resetMsg); ok {
				return rm
			}
		}
	}
	t.Fatalf("got %T, want resetMsg", msg)
	return resetMsg{}
}

// TestPickerCopyKeyRunsTransferWithoutAsking: [x] never touches a source, so
// it runs immediately — no modal in the way, unlike [X].
func TestPickerCopyKeyRunsTransferWithoutAsking(t *testing.T) {
	m, _ := pickerFixture(t)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	p := next.(pickerModel)
	if !p.transferring || p.askMove {
		t.Fatalf("[x] should start transferring immediately, got transferring=%v askMove=%v", p.transferring, p.askMove)
	}
	if cmd == nil {
		t.Fatal("[x] dispatched no command")
	}

	msg := drainTransferred(t, cmd())
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	// pickerFixture's seeded rows have no real file on disk, so the transfer
	// fails at the source-stat step — that failure landing back is exactly
	// what proves [x] really drove execute.Run rather than being a no-op key.
	if msg.rep.Failed == 0 {
		t.Fatal("expected the transfer to report a failure for the fixture's fake source path")
	}

	done := p.transferred(msg)
	if done.transferring {
		t.Error("still transferring after the result landed")
	}
	if !done.statusErr || done.status == "" {
		t.Error("a failed transfer did not report a status")
	}
}

// TestPickerMoveKeyAsksFirst: [X] deletes files, so unlike [x] it raises a
// question, defaults to Cancel, and only starts on an explicit yes.
func TestPickerMoveKeyAsksFirst(t *testing.T) {
	m, _ := pickerFixture(t)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	p := next.(pickerModel)
	if !p.askMove || p.moveChoice {
		t.Fatalf("[X] should ask with Cancel as the default, got askMove=%v moveChoice=%v", p.askMove, p.moveChoice)
	}
	if cmd != nil {
		t.Fatal("[X] should not start anything before the question is answered")
	}

	// "n" cancels — nothing starts
	next, cmd = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	p = next.(pickerModel)
	if p.askMove {
		t.Error("[n] should dismiss the ask")
	}
	if p.transferring || cmd != nil {
		t.Fatal("[n] must not start a transfer")
	}

	// ask again, this time say yes
	next, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	next, cmd = next.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	p = next.(pickerModel)
	if p.askMove || !p.transferring {
		t.Fatal("[y] must start the move on that press")
	}
	if cmd == nil {
		t.Fatal("[y] dispatched no command")
	}
	msg := drainTransferred(t, cmd())
	if msg.mode != execute.ModeMove {
		t.Errorf("mode = %v, want ModeMove", msg.mode)
	}
}

// TestPickerTransferProgressFeedsTheBar: execute reports one file at a time,
// so the picker has to accumulate the bytes and re-arm the channel read —
// without the re-arm the bar freezes on the first file it ever drew.
func TestPickerTransferProgressFeedsTheBar(t *testing.T) {
	m, _ := pickerFixture(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	p := next.(pickerModel)
	p.w, p.h = 100, 24

	next, cmd := p.Update(transferProgressMsg{target: "2024/01/a.jpg", bytes: 1024, done: 1, total: 2})
	if cmd == nil {
		t.Error("a progress report must re-arm the channel read")
	}
	next, _ = next.(pickerModel).Update(transferProgressMsg{target: "2024/01/b.jpg", bytes: 1024, done: 2, total: 2})
	p = next.(pickerModel)
	if p.bytes != 2048 {
		t.Errorf("bytes = %d, want 2048 — reports are per file, not cumulative", p.bytes)
	}
	if !strings.Contains(p.View(), "2/2") {
		t.Error("the progress row does not say how many files are done")
	}

	// A report that arrives after the result must not put the bar back up.
	done := p.transferred(transferredMsg{})
	next, _ = done.Update(transferProgressMsg{done: 9, total: 9})
	if next.(pickerModel).prog.done == 9 {
		t.Error("a late report was drawn after the transfer had already reported")
	}
}

func drainTransferred(t *testing.T, msg tea.Msg) transferredMsg {
	t.Helper()
	switch m := msg.(type) {
	case transferredMsg:
		return m
	case tea.BatchMsg:
		for _, c := range m {
			if c == nil {
				continue
			}
			if tm, ok := c().(transferredMsg); ok {
				return tm
			}
		}
	}
	t.Fatalf("got %T, want transferredMsg", msg)
	return transferredMsg{}
}

// TestPickerReenterCountsSavedSlices: the outcome a caller reports is "did any
// slice get saved", which is what reenter tallies as each one comes back.
func TestPickerReenterCountsSavedSlices(t *testing.T) {
	m, _ := pickerFixture(t)
	if confirmed, _, _ := Outcome(m); confirmed {
		t.Error("a fresh picker reports a confirmed review")
	}

	back, err := m.reenter()
	if err != nil {
		t.Fatal(err)
	}
	if confirmed, _, ok := Outcome(back); !ok || !confirmed {
		t.Errorf("Outcome after a saved slice = %v (ok %v), want confirmed", confirmed, ok)
	}
}
