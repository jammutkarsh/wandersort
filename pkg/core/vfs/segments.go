// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

// segments.go slices the proposal into time ranges so a library of 400 folders
// is reviewed and saved a few at a time instead of all-or-nothing. A segment is
// a range over virtual_fs_entries.taken_at — the folder date the vfs phase
// wrote, which no rename can move — so a file's segment never changes under
// the reviewer's own edits, and because every member of a cluster shares that
// date, a boundary can never split one event in two.

import (
	"context"
	"fmt"
	"maps"
	gopath "path"
	"slices"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/db"
)

// Segment is one reviewable slice of the proposal. Start/End are half-open
// [Start, End); both nil is the undated bucket (files with no capture time,
// which land in the Fallback folder).
type Segment struct {
	Label              string
	Start, End         *time.Time
	Proposed, Approved int
	// Folders is how many directories this slice's tree holds — every level of
	// it, the same count the review screen's header reports. A reviewer decides
	// about folders, not about files, so it is the number the picker leads with.
	Folders int
}

// UndatedLabel names the bucket for files with no capture time. They have no
// place on a timeline, but they still have to be reviewable.
const UndatedLabel = "Undated"

// autoLongSpanYears is when a library is long enough that half-year slices
// would give the picker a list nobody scrolls: past this, segment by year.
const autoLongSpanYears = 3

// Segments buckets the still-reviewable proposal into calendar-aligned time
// slices. months <= 0 picks between yearly and half-yearly from the span the
// library actually covers; otherwise it is the wizard's setting (3, 6 or 12).
//
// A nil result means "don't segment": one slice is not a choice, and neither
// is a library with nothing dated in it.
func Segments(ctx context.Context, database *db.DB, months int) ([]Segment, error) {
	var rows []struct {
		TakenAt    *string `db:"taken_at"`
		Status     string  `db:"status"`
		TargetPath string  `db:"target_path"`
	}
	if err := database.SQL.SelectContext(ctx, &rows,
		`SELECT taken_at, status, target_path FROM virtual_fs_entries WHERE status IN (?, ?)`,
		db.StatusProposed, db.StatusApproved); err != nil {
		return nil, fmt.Errorf("query proposal dates: %w", err)
	}

	type entry struct {
		at     time.Time
		dated  bool
		status string
		dir    string
	}
	entries := make([]entry, 0, len(rows))
	var lo, hi time.Time
	for _, r := range rows {
		e := entry{status: r.Status, dir: gopath.Dir(r.TargetPath)}
		if r.TakenAt != nil {
			// written by persist through db.FormatTime, so anything that
			// doesn't parse is a foreign row — treat it as undated rather
			// than failing a review over it
			if t, err := time.Parse(db.TimeLayout, *r.TakenAt); err == nil {
				e.at, e.dated = t, true
				if lo.IsZero() || t.Before(lo) {
					lo = t
				}
				if t.After(hi) {
					hi = t
				}
			}
		}
		entries = append(entries, e)
	}
	if lo.IsZero() {
		return nil, nil // nothing dated: a single "Undated" pile is not a picker
	}

	if months <= 0 {
		months = 6
		if hi.Sub(lo) > autoLongSpanYears*365*24*time.Hour {
			months = 12
		}
	}

	buckets := map[time.Time]*Segment{}
	var undated *Segment
	// per-segment set of every directory its tree will hold, ancestors included
	// — the same thing BuildTree counts, without building it
	folders := map[*Segment]map[string]bool{}
	for _, e := range entries {
		seg := undated
		if e.dated {
			start := bucketStart(e.at, months)
			seg = buckets[start]
			if seg == nil {
				end := start.AddDate(0, months, 0)
				seg = &Segment{Label: segmentLabel(start, months), Start: &start, End: &end}
				buckets[start] = seg
			}
		} else if seg == nil {
			seg = &Segment{Label: UndatedLabel}
			undated = seg
		}
		if e.status == db.StatusApproved {
			seg.Approved++
		} else {
			seg.Proposed++
		}
		seen := folders[seg]
		if seen == nil {
			seen = map[string]bool{}
			folders[seg] = seen
		}
		// most rows repeat a directory a sibling already added, so the walk up
		// the ancestors stops at the first one already counted
		for d := e.dir; d != "" && d != "." && d != "/" && !seen[d]; d = gopath.Dir(d) {
			seen[d] = true
		}
	}
	for seg, seen := range folders {
		seg.Folders = len(seen)
	}

	out := make([]Segment, 0, len(buckets)+1)
	for _, start := range slices.SortedFunc(maps.Keys(buckets), time.Time.Compare) {
		out = append(out, *buckets[start])
	}
	// undated last: it is the leftovers, not the start of the timeline
	if undated != nil {
		out = append(out, *undated)
	}
	if len(out) < 2 {
		return nil, nil
	}
	return out, nil
}

// bucketStart is the calendar-aligned start of the slice t falls in: January
// for years, Jan/Jul for half-years, Jan/Apr/Jul/Oct for quarters. Aligned to
// the calendar rather than to the library's first photo so the labels read as
// dates a person recognises.
func bucketStart(t time.Time, months int) time.Time {
	mon := int(t.Month()) - 1
	return time.Date(t.Year(), time.Month(mon-mon%months+1), 1, 0, 0, 0, 0, time.UTC)
}

// segmentLabel names a slice by what it covers: "2023" for a whole year,
// "2024 Jan–Jun" for anything shorter.
func segmentLabel(start time.Time, months int) string {
	if months >= 12 {
		return start.Format("2006")
	}
	last := start.AddDate(0, months-1, 0)
	return fmt.Sprintf("%s %s–%s", start.Format("2006"), start.Format("Jan"), last.Format("Jan"))
}

// clause restricts a query to this segment: the whole proposal for a nil
// segment, the undated rows for the undated bucket, the half-open range
// otherwise. Returns SQL to AND onto a WHERE, or "" for no restriction.
func (s *Segment) clause(col string) (string, []any) {
	switch {
	case s == nil:
		return "", nil
	case s.Start == nil || s.End == nil:
		return col + " IS NULL", nil
	default:
		return col + " >= ? AND " + col + " < ?", []any{db.FormatTime(*s.Start), db.FormatTime(*s.End)}
	}
}

// ReopenSegment flips a saved segment's entries back to PROPOSED, so a
// reviewer who changed their mind can redo that slice without rebuilding the
// whole proposal. A nil seg reopens the whole library — what a rebuild does
// first, since re-proposing replaces the folders an approval was given for.
func ReopenSegment(ctx context.Context, database *db.DB, seg *Segment) error {
	if err := setSegmentStatus(ctx, database, seg, db.StatusApproved, db.StatusProposed); err != nil {
		return fmt.Errorf("reopen %s: %w", segName(seg), err)
	}
	return nil
}

// ApproveSegment signs a slice off exactly as proposed, without opening it.
// Confirm is the same flip plus the reviewer's edits; with nothing edited
// there are no paths to rewrite and no names to learn, so this is all of it.
func ApproveSegment(ctx context.Context, database *db.DB, seg *Segment) error {
	if err := setSegmentStatus(ctx, database, seg, db.StatusProposed, db.StatusApproved); err != nil {
		return fmt.Errorf("approve %s: %w", segName(seg), err)
	}
	return nil
}

func setSegmentStatus(ctx context.Context, database *db.DB, seg *Segment, from, to string) error {
	where, args := seg.clause("taken_at")
	if where != "" {
		where = " AND " + where
	}
	return database.Writer.WriteSync(func(ctx context.Context, tx *sqlx.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE virtual_fs_entries SET status = ? WHERE status = ?`+where,
			append([]any{to, from}, args...)...)
		return err
	})
}

func segName(seg *Segment) string {
	if seg == nil {
		return "the whole plan"
	}
	return strconv.Quote(seg.Label)
}
