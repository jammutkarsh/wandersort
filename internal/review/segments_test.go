// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

	o := Options{DB: d, Log: logger.NewNoopLogger(), SegmentMonths: 12}
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

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
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
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
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
	next, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	next, _ = next.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
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

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	_, cmd := next.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	s := drainOpen(t, cmd()).model.(screen)

	back, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if b := back.(screen); b.confirmed {
		t.Error("[esc] confirmed the slice")
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

// TestSegmentRebuildStaysScoped: [R] re-proposes the whole library, but the
// tree it hands back must still be this one slice — otherwise a reset inside
// 2017 replaces it with every year at once.
func TestSegmentRebuildStaysScoped(t *testing.T) {
	m, d := pickerFixture(t)
	var got *vfs.Segment
	m.o.Rebuild = func(_ context.Context, seg *vfs.Segment) ([]vfs.Node, error) {
		got = seg
		return vfs.BuildTree(context.Background(), d, seg)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	_, cmd := next.(pickerModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	s := drainOpen(t, cmd()).model.(screen)

	inner, cmd := s.inner.startRebuild()
	if cmd == nil {
		t.Fatal("rebuild dispatched nothing")
	}
	msg := drainRebuilt(t, cmd())
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if got == nil || got.Label != "2024" {
		t.Fatalf("rebuild asked for %+v, want the 2024 slice", got)
	}
	tree := inner.(Model).rebuilt(msg).tree
	if len(tree) != 1 || tree[0].Name != "2024" {
		t.Errorf("rebuilt tree = %+v, want only the 2024 slice", tree)
	}
}

func drainRebuilt(t *testing.T, msg tea.Msg) rebuiltMsg {
	t.Helper()
	switch m := msg.(type) {
	case rebuiltMsg:
		return m
	case tea.BatchMsg:
		for _, c := range m {
			if c == nil {
				continue
			}
			if rm, ok := c().(rebuiltMsg); ok {
				return rm
			}
		}
	}
	t.Fatalf("got %T, want rebuiltMsg", msg)
	return rebuiltMsg{}
}

// TestPickerRebuildIsGlobalAndAutoAsks: a settings change while the picker is
// on screen must raise the same reset question the per-segment screen shows —
// without this, noticing a settings change meant opening every slice by hand.
// Answering "y" re-proposes the whole library (seg == nil), not one slice.
func TestPickerRebuildIsGlobalAndAutoAsks(t *testing.T) {
	m, _ := pickerFixture(t)

	// no Rebuild wired up: [R] must not raise a question it can't answer
	if next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")}); next.(pickerModel).askRebuild {
		t.Fatal("[R] raised the question with no way to rebuild")
	}

	var got *vfs.Segment
	calls := 0
	m.o.Rebuild = func(_ context.Context, seg *vfs.Segment) ([]vfs.Node, error) {
		got, calls = seg, calls+1
		return nil, nil
	}

	next, _ := m.Update(SettingsChangedMsg{})
	p := next.(pickerModel)
	if !p.askRebuild || !p.askedBySettings {
		t.Fatal("a settings change must raise the rebuild question")
	}

	next, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	p = next.(pickerModel)
	if p.askRebuild || !p.rebuilding || cmd == nil {
		t.Fatal("y must start the rebuild on that press")
	}

	msg := drainLibraryRebuilt(t, cmd())
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if calls != 1 || got != nil {
		t.Fatalf("rebuild called %d times with seg %+v, want once with nil (library-wide)", calls, got)
	}

	final := p.rebuilt(msg)
	if final.rebuilding {
		t.Error("still rebuilding after the result landed")
	}
	if final.statusErr {
		t.Errorf("rebuild reported an error: %s", final.status)
	}
}

func drainLibraryRebuilt(t *testing.T, msg tea.Msg) libraryRebuiltMsg {
	t.Helper()
	switch m := msg.(type) {
	case libraryRebuiltMsg:
		return m
	case tea.BatchMsg:
		for _, c := range m {
			if c == nil {
				continue
			}
			if lm, ok := c().(libraryRebuiltMsg); ok {
				return lm
			}
		}
	}
	t.Fatalf("got %T, want libraryRebuiltMsg", msg)
	return libraryRebuiltMsg{}
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
