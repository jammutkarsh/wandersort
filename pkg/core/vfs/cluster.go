// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

// cluster groups masters whose capture times sit within the configured gap of
// each other — one cluster ≈ one real-world event
type cluster struct {
	members    []int // indices into masters
	start, end time.Time
}

// clusterAndSpill groups files by capture-time gap and gives a cluster with
// *nothing* located a dated event segment instead of a location folder.
//
// It used to also spill one member's GPS city over the cluster's GPS-less
// members. That invented a place: a 12h cluster is most of a day, so a DSLR
// shot nine hours after a phone photo inherited the phone's city — and near a
// saved place, which is where most GPS-less files sit, it named every one of
// them after the saved place. A GPS-less file now keeps no location and
// markUnknownLocations (plan.go) puts it in an Unknown folder beside its
// located siblings, which says what is actually known.
func clusterAndSpill(masters []masterFile, gap time.Duration) {
	if gap <= 0 {
		gap = defaultClusterGap
	}

	sortByCaptureTime(masters)

	var clusters []cluster
	for i := range masters {
		t := masters[i].takenAt
		if len(clusters) == 0 || t.Sub(clusters[len(clusters)-1].end) > gap {
			clusters = append(clusters, cluster{start: t, end: t})
		}
		c := &clusters[len(clusters)-1]
		c.members = append(c.members, i)
		c.end = t
	}

	clusterNum := 0
	for ci := range clusters {
		c := &clusters[ci]

		located := 0
		for _, i := range c.members {
			if masters[i].location != "" {
				located++
			}
		}
		if located == len(c.members) {
			continue // nothing unlocated to decide
		}
		if located > 0 {
			// mixed cluster: the GPS-less members get an Unknown folder next to
			// their located siblings, not a place borrowed from them
			continue
		}

		clusterNum++
		id := fmt.Sprintf("c%d", clusterNum)

		// nothing located: fall back to a dated segment. No member here is
		// atSavedPlace — that always carries a real location.
		seg := eventSegment(c.start, c.end)
		for _, i := range c.members {
			masters[i].clusterID = id
			masters[i].eventSegment = seg
		}
	}
}

// sortKey is one master reduced to what the sort compares: the capture instant
// plus the original index. 24 bytes against masterFile's 408, so a 100k library
// sorts inside L2 instead of chasing 41 MB of struct.
type sortKey struct {
	sec  int64
	idx  int   // indexes masters, so it holds whatever a slice can
	nsec int32 // nanosecond-within-second, 0..999999999 by definition
}

// sortByCaptureTime orders masters oldest-first, stably. It sorts compact keys
// and applies the permutation once: masterFile is a wide struct, so both moving
// it and reaching through an index to read one time field off it touch cache
// lines the comparison never needs.
func sortByCaptureTime(masters []masterFile) {
	keys := make([]sortKey, len(masters))
	for i := range masters {
		takenAt := masters[i].takenAt
		// Unix()+Nanosecond() orders identically to takenAt.Compare (no
		// monotonic readings here — every time comes from time.Parse) and,
		// unlike UnixNano, doesn't overflow on an undated file's zero time.
		keys[i] = sortKey{sec: takenAt.Unix(), idx: i, nsec: int32(takenAt.Nanosecond())}
	}
	// carrying idx in the key makes an unstable sort stable, which is what lets
	// this use the faster SortFunc
	slices.SortFunc(keys, func(a, b sortKey) int {
		if a.sec != b.sec {
			return cmp.Compare(a.sec, b.sec)
		}
		if a.nsec != b.nsec {
			return cmp.Compare(a.nsec, b.nsec)
		}
		return cmp.Compare(a.idx, b.idx)
	})
	// Apply the permutation in place, following one cycle at a time: each
	// master moves exactly once, and idx is reset to its own slot as it goes so
	// the outer loop skips what a cycle already placed. The scratch-slice
	// version of this had to zero 41 MB at 100k before writing a byte to it.
	for k := range keys {
		if keys[k].idx == k {
			continue
		}
		held := masters[k] // the one element a cycle can't move into place directly
		cur := k
		for {
			src := keys[cur].idx
			if src == k { // cycle closed — its last slot wants what we lifted out
				break
			}
			masters[cur] = masters[src]
			keys[cur].idx = cur
			cur = src
		}
		masters[cur] = held
		keys[cur].idx = cur
	}
}

// eventSegment renders the dated placeholder for an unresolved cluster. It
// sits under the month folder, so days alone suffice ("03", "03-05"); only a
// cross-month span keeps month names ("Jun_30-Jul_01").
func eventSegment(start, end time.Time) string {
	switch {
	case start.Year() == end.Year() && start.YearDay() == end.YearDay():
		return start.Format("02")
	case start.Year() == end.Year() && start.Month() == end.Month():
		return start.Format("02") + "-" + end.Format("02")
	default:
		return start.Format("Jan_02") + "-" + end.Format("Jan_02")
	}
}
