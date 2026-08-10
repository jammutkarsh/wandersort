// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
)

// seedEntry inserts one proposal row with a chosen folder date, so a segment
// test can state the timeline directly instead of driving the whole pipeline.
func seedEntry(t *testing.T, d *db.DB, id int64, target string, takenAt time.Time, status string) {
	t.Helper()
	name := fmt.Sprintf("IMG_%d.jpg", id)
	dbtest.SeedFile(t, d, id, "/src", name, 100)
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO file_metadata (file_hash, file_id) VALUES (?, ?)`,
		fmt.Sprintf("hash-%d", id), id); err != nil {
		t.Fatal(err)
	}
	var at any
	if !takenAt.IsZero() {
		at = db.FormatTime(takenAt)
	}
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO virtual_fs_entries (file_id, source_path, target_path, status, taken_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, "/src/"+name, target+"/"+name, status, at); err != nil {
		t.Fatal(err)
	}
}

func day(y int, mon time.Month, d int) time.Time {
	return time.Date(y, mon, d, 12, 0, 0, 0, time.UTC)
}

func labels(segs []Segment) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.Label
	}
	return out
}

func TestSegmentsHeuristic(t *testing.T) {
	tests := []struct {
		name   string
		months int
		dates  []time.Time
		want   []string
	}{
		{"two-year span slices by half-year", 0, []time.Time{
			day(2023, time.February, 1), day(2023, time.September, 1), day(2024, time.March, 1),
		}, []string{"2023 Jan–Jun", "2023 Jul–Dec", "2024 Jan–Jun"}},
		{"long span slices by year", 0, []time.Time{
			day(2020, time.February, 1), day(2022, time.June, 1), day(2024, time.March, 1),
		}, []string{"2020", "2022", "2024"}},
		{"explicit quarters", 3, []time.Time{
			day(2024, time.February, 1), day(2024, time.May, 1),
		}, []string{"2024 Jan–Mar", "2024 Apr–Jun"}},
		{"one slice is not a choice", 0, []time.Time{
			day(2024, time.February, 1), day(2024, time.March, 1),
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := dbtest.New(t)
			for i, at := range tt.dates {
				seedEntry(t, d, int64(i+1), "2024/x", at, db.StatusProposed)
			}
			segs, err := Segments(context.Background(), d, tt.months)
			if err != nil {
				t.Fatal(err)
			}
			got := labels(segs)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("labels = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSegmentsUndatedBucketAndCounts(t *testing.T) {
	d := dbtest.New(t)
	seedEntry(t, d, 1, "2023/x", day(2023, time.March, 1), db.StatusApproved)
	seedEntry(t, d, 2, "2024/x", day(2024, time.March, 1), db.StatusProposed)
	seedEntry(t, d, 3, "Unsorted", time.Time{}, db.StatusProposed)

	segs, err := Segments(context.Background(), d, 12)
	if err != nil {
		t.Fatal(err)
	}
	if got := labels(segs); strings.Join(got, "|") != "2023|2024|Undated" {
		t.Fatalf("labels = %v, want [2023 2024 Undated] (undated last)", got)
	}
	if segs[0].Approved != 1 || segs[0].Proposed != 0 {
		t.Errorf("2023 counts = %d approved / %d proposed, want 1/0", segs[0].Approved, segs[0].Proposed)
	}
	if segs[2].Start != nil || segs[2].End != nil {
		t.Errorf("undated segment carries a range: %+v", segs[2])
	}
}

// TestSegmentsIgnoresExecutedRows: a DONE row is past reviewing, so it must
// not show up as work left in a slice.
func TestSegmentsIgnoresExecutedRows(t *testing.T) {
	d := dbtest.New(t)
	seedEntry(t, d, 1, "2023/x", day(2023, time.March, 1), db.StatusProposed)
	seedEntry(t, d, 2, "2024/x", day(2024, time.March, 1), "DONE")

	segs, err := Segments(context.Background(), d, 12)
	if err != nil {
		t.Fatal(err)
	}
	if segs != nil {
		t.Errorf("segments = %v, want nil (only one reviewable slice)", labels(segs))
	}
}

// entryStatuses reads back what every proposal row ended up as, keyed by file.
func entryStatuses(t *testing.T, d *db.DB) map[int64]struct{ Path, Status string } {
	t.Helper()
	var rows []struct {
		FileID     int64  `db:"file_id"`
		TargetPath string `db:"target_path"`
		Status     string `db:"status"`
	}
	if err := d.SQL.Select(&rows, `SELECT file_id, target_path, status FROM virtual_fs_entries`); err != nil {
		t.Fatal(err)
	}
	out := map[int64]struct{ Path, Status string }{}
	for _, r := range rows {
		out[r.FileID] = struct{ Path, Status string }{r.TargetPath, r.Status}
	}
	return out
}

// TestConfirmScopedToSegment is the whole point of segmenting: saving one
// slice approves that slice only — and a rename made in it still carries onto
// the rows of every other slice sitting under the renamed folder.
func TestConfirmScopedToSegment(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	seedEntry(t, d, 1, "2023/03_March/Goa", day(2023, time.March, 1), db.StatusProposed)
	seedEntry(t, d, 2, "2024/03_March/Goa", day(2024, time.March, 1), db.StatusProposed)

	segs, err := Segments(ctx, d, 12)
	if err != nil || len(segs) != 2 {
		t.Fatalf("Segments = %v, %v; want two slices", labels(segs), err)
	}

	tree, err := BuildTree(ctx, d, &segs[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 1 || tree[0].Name != "2023" {
		t.Fatalf("segment tree = %+v, want just 2023", tree)
	}
	// rename the Year node: nothing below it is in this segment's tree, but
	// 2024's rows are not affected by it either — different Year node
	tree[0].Children[0].Name = "March"

	if err := Confirm(ctx, d, tree, &segs[0]); err != nil {
		t.Fatal(err)
	}
	got := entryStatuses(t, d)
	if got[1].Status != db.StatusApproved {
		t.Errorf("2023 entry status = %q, want APPROVED", got[1].Status)
	}
	if got[2].Status != db.StatusProposed {
		t.Errorf("2024 entry status = %q, want still PROPOSED", got[2].Status)
	}
	if !strings.HasPrefix(got[1].Path, "2023/March/Goa/") {
		t.Errorf("2023 target = %q, want the renamed month", got[1].Path)
	}
	if !strings.HasPrefix(got[2].Path, "2024/03_March/Goa/") {
		t.Errorf("2024 target = %q, want untouched", got[2].Path)
	}
}

// TestConfirmRenameCarriesOntoOtherSegments is the prefix rewrite: a segment's
// tree holds the Year node, but the *rows* under it belong to other slices too
// — they have to follow the rename or one year becomes two folders on disk.
func TestConfirmRenameCarriesOntoOtherSegments(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	// same year, two half-year slices
	seedEntry(t, d, 1, "2024/02_February/Goa", day(2024, time.February, 1), db.StatusProposed)
	seedEntry(t, d, 2, "2024/09_September/Goa", day(2024, time.September, 1), db.StatusProposed)

	segs, err := Segments(ctx, d, 6)
	if err != nil || len(segs) != 2 {
		t.Fatalf("Segments = %v, %v; want two slices", labels(segs), err)
	}
	tree, err := BuildTree(ctx, d, &segs[0])
	if err != nil {
		t.Fatal(err)
	}
	tree[0].Name = "Twenty24" // the Year node

	if err := Confirm(ctx, d, tree, &segs[0]); err != nil {
		t.Fatal(err)
	}
	got := entryStatuses(t, d)
	if !strings.HasPrefix(got[2].Path, "Twenty24/09_September/") {
		t.Errorf("out-of-segment row = %q, want the renamed year with its own month kept", got[2].Path)
	}
	if got[2].Status != db.StatusProposed {
		t.Errorf("out-of-segment row status = %q, want still PROPOSED", got[2].Status)
	}
}

func TestReopenSegment(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	seedEntry(t, d, 1, "2023/x", day(2023, time.March, 1), db.StatusApproved)
	seedEntry(t, d, 2, "2024/x", day(2024, time.March, 1), db.StatusApproved)

	segs, err := Segments(ctx, d, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReopenSegment(ctx, d, &segs[0]); err != nil {
		t.Fatal(err)
	}
	got := entryStatuses(t, d)
	if got[1].Status != db.StatusProposed {
		t.Errorf("reopened entry status = %q, want PROPOSED", got[1].Status)
	}
	if got[2].Status != db.StatusApproved {
		t.Errorf("other segment status = %q, want still APPROVED", got[2].Status)
	}
}

// TestSegmentsCountsFolders: the picker leads with folders, since a review is
// a decision about folders — every level of the tree, the same thing
// BuildTree's nodes count, not just the directory a file sits in.
func TestSegmentsCountsFolders(t *testing.T) {
	d := dbtest.New(t)
	seedEntry(t, d, 1, "2023/03_March/01/Goa", day(2023, time.March, 1), db.StatusProposed)
	seedEntry(t, d, 2, "2023/03_March/01/Goa", day(2023, time.March, 1), db.StatusProposed)
	seedEntry(t, d, 3, "2023/03_March/02/Pune", day(2023, time.March, 2), db.StatusProposed)
	seedEntry(t, d, 4, "2024/05_May/09", day(2024, time.May, 9), db.StatusProposed)

	segs, err := Segments(context.Background(), d, 12)
	if err != nil {
		t.Fatal(err)
	}
	// 2023, 03_March, 01, Goa, 02, Pune
	if segs[0].Folders != 6 {
		t.Errorf("2023 folders = %d, want 6", segs[0].Folders)
	}
	// 2024, 05_May, 09
	if segs[1].Folders != 3 {
		t.Errorf("2024 folders = %d, want 3", segs[1].Folders)
	}
}

// TestReopenWholeLibrary is what a rebuild does first: the plan an approval was
// given for is about to be replaced, so every slice goes back to reviewable —
// a saved slice still reading "✓ saved" after a settings change and a reset was
// a reported bug.
func TestReopenWholeLibrary(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	seedEntry(t, d, 1, "2023/x", day(2023, time.March, 1), db.StatusApproved)
	seedEntry(t, d, 2, "2024/x", day(2024, time.March, 1), db.StatusApproved)

	if err := ReopenSegment(ctx, d, nil); err != nil {
		t.Fatal(err)
	}
	for id, e := range entryStatuses(t, d) {
		if e.Status != db.StatusProposed {
			t.Errorf("entry %d status = %q, want PROPOSED", id, e.Status)
		}
	}
}

// TestApproveSegment: [A] on the picker signs a slice off without opening it,
// which is how an untouched slice gets saved now that [esc] over an unedited
// tree just steps back.
func TestApproveSegment(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	seedEntry(t, d, 1, "2023/x", day(2023, time.March, 1), db.StatusProposed)
	seedEntry(t, d, 2, "2024/x", day(2024, time.March, 1), db.StatusProposed)

	segs, err := Segments(ctx, d, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApproveSegment(ctx, d, &segs[0]); err != nil {
		t.Fatal(err)
	}
	got := entryStatuses(t, d)
	if got[1].Status != db.StatusApproved {
		t.Errorf("accepted entry status = %q, want APPROVED", got[1].Status)
	}
	if got[2].Status != db.StatusProposed {
		t.Errorf("other segment status = %q, want still PROPOSED", got[2].Status)
	}
}

// TestPersistKeepsApprovedRows: a rebuild re-proposes what nobody signed off
// and leaves an approved plan — edited paths included — exactly as it is.
func TestPersistKeepsApprovedRows(t *testing.T) {
	h := newHarness(t)
	h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2023:06:03 14:00:00", 0, 0, 3024, 4032))
	h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
	cfg := DefaultConfig()
	h.build(t, cfg, nil)

	// approve file 1 under a path of its own, as a saved segment would
	if _, err := h.d.ExecContext(context.Background(),
		`UPDATE virtual_fs_entries SET status = ?, target_path = 'Saved/A.HEIC' WHERE file_id = 1`,
		db.StatusApproved); err != nil {
		t.Fatal(err)
	}

	h.build(t, cfg, nil) // rebuild
	got := entryStatuses(t, h.d)
	if got[1].Status != db.StatusApproved || got[1].Path != "Saved/A.HEIC" {
		t.Errorf("approved entry = %+v, want kept as-is", got[1])
	}
	if got[2].Status != db.StatusProposed {
		t.Errorf("unapproved entry = %+v, want re-proposed", got[2])
	}
}

// TestPersistPrunesApprovedRowsForDeadMasters: an approved plan for a file
// that is no longer in the library promises a move that can't happen.
func TestPersistPrunesApprovedRowsForDeadMasters(t *testing.T) {
	h := newHarness(t)
	h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
	h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:03 15:00:00", 0, 0, 3024, 4032))
	cfg := DefaultConfig()
	h.build(t, cfg, nil)

	if _, err := h.d.ExecContext(context.Background(),
		`UPDATE virtual_fs_entries SET status = ? WHERE file_id = 1`, db.StatusApproved); err != nil {
		t.Fatal(err)
	}
	// file 1 vanishes from the library (soft-deleted by a later scan)
	if _, err := h.d.ExecContext(context.Background(),
		`UPDATE file_registry SET deleted_at = ? WHERE id = 1`, db.FormatTime(time.Now())); err != nil {
		t.Fatal(err)
	}

	h.build(t, cfg, nil)
	if got := entryStatuses(t, h.d); len(got) != 1 || got[2].Status != db.StatusProposed {
		t.Errorf("entries after rebuild = %+v, want only the live master's proposal", got)
	}
}
