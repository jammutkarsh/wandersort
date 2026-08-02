// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/location"
)

// cluster groups masters whose capture times sit within the configured gap of
// each other — one cluster ≈ one real-world event
type cluster struct {
	members    []int // indices into masters
	start, end time.Time
}

// clusterAndSuggest handles files with no location of their own: time-gap
// clustering, GPS spillover within a cluster, then a ranked name suggestion for
// whatever is still unresolved.
func clusterAndSuggest(masters []masterFile, labels []userLabel, anchors []location.Anchor, gap time.Duration) {
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

	anchorCities := make([]string, 0, len(anchors))
	seen := map[string]bool{}
	for _, a := range anchors {
		if !seen[a.FolderName] {
			seen[a.FolderName] = true
			anchorCities = append(anchorCities, a.FolderName)
		}
	}

	clusterNum := 0
	for ci := range clusters {
		c := &clusters[ci]
		city, cityIsSavedPlace := majorityCity(masters, c.members)

		var unlocated []int
		for _, i := range c.members {
			if masters[i].location == "" {
				unlocated = append(unlocated, i)
			}
		}
		if len(unlocated) == 0 {
			continue // every member already located (saved-place included); nothing to decide
		}

		clusterNum++
		id := fmt.Sprintf("c%d", clusterNum)

		// spillover: one located member names the whole event. A GPS-less file
		// clustered with saved-place photos inherits atSavedPlace too — it's
		// almost certainly the same everyday place (an indoor shot with no fix).
		if city != "" {
			for _, i := range unlocated {
				masters[i].location = city
				masters[i].atSavedPlace = cityIsSavedPlace // SavedPlacesDateOnly must suppress its folder here too, not just for its located neighbours
				masters[i].clusterID = id
				masters[i].suggestion = city
				masters[i].suggestionSource = SuggestionSpillover
			}
			continue
		}

		// nothing located: fall back to a dated segment plus a name suggestion.
		// No member here is atSavedPlace — that always carries a real location
		// now, which would have made city non-empty above.
		seg := eventSegment(c.start, c.end)
		sug, src := suggestFor(masters, c, labels, anchorCities)
		for _, i := range c.members {
			masters[i].clusterID = id
			masters[i].eventSegment = seg
			masters[i].suggestion = sug
			masters[i].suggestionSource = src
		}
	}
}

// sortKey is one master reduced to what the sort actually compares: the
// capture instant, plus the original index. 24 bytes against masterFile's 408,
// so a 100k library sorts inside L2 instead of chasing 41 MB of struct. idx is
// a plain int — it indexes masters, so it holds whatever a slice can, and this
// imposes no limit of its own. nsec is int32 because a nanosecond-within-second
// is 0..999999999 by definition, not because anything is being capped.
type sortKey struct {
	sec  int64
	idx  int
	nsec int32
}

// sortByCaptureTime orders masters oldest-first, stably. It sorts compact keys
// and applies the permutation once: masterFile is a wide struct, so both moving
// it and reaching through an index to read one time field off it touch cache
// lines the comparison never needs.
func sortByCaptureTime(masters []masterFile) {
	keys := make([]sortKey, len(masters))
	for i := range masters {
		t := masters[i].takenAt
		// Unix()+Nanosecond() is the same instant ordering takenAt.Compare
		// gives — none of these times carry a monotonic reading, they all come
		// from time.Parse — and unlike UnixNano it doesn't overflow on the zero
		// time an undated file has.
		keys[i] = sortKey{sec: t.Unix(), idx: i, nsec: int32(t.Nanosecond())}
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

// majorityCity returns the most common resolved city among members, plus
// whether it got there via atSavedPlace — location alone can't tell a
// folded saved-place name from a plain resolved one.
func majorityCity(masters []masterFile, members []int) (city string, atSavedPlace bool) {
	counts := map[string]int{}
	home := map[string]bool{}
	best, bestN := "", 0
	for _, i := range members {
		city := masters[i].location
		if city == "" {
			continue
		}
		if masters[i].atSavedPlace {
			home[city] = true
		}
		counts[city]++
		if counts[city] > bestN {
			best, bestN = city, counts[city]
		}
	}
	return best, home[best]
}

// suggestFor ranks a name suggestion for a fully unresolved cluster:
// confirmed label overlapping in time → anchor city → the user's own
// (non-generic) source folder name.
func suggestFor(masters []masterFile, c *cluster, labels []userLabel, anchors []string) (string, string) {
	for _, l := range labels {
		if l.Kind != "EVENT" {
			continue
		}
		ls, okS := parseTimeLoose(deref(l.TimeStart))
		le, okE := parseTimeLoose(deref(l.TimeEnd))
		if okS && okE && ls.Before(c.end.Add(time.Second)) && le.After(c.start.Add(-time.Second)) {
			return l.Label, SuggestionUserLabel
		}
	}

	if len(anchors) > 0 {
		return anchors[0], SuggestionAnchor
	}

	// most common meaningful source folder among the members. Only the
	// immediate parent is judged: generic segments higher up (Users, Downloads)
	// must not disqualify a meaningful leaf folder.
	counts := map[string]int{}
	// IsGenericDirName is a regexp match; a cluster's members mostly share a
	// handful of folders, so judge each distinct one once. A memo rather than a
	// pass over counts: the tie below is broken by member order, and ranging a
	// map would make the winner vary run to run.
	generic := map[string]bool{}
	best, bestN := "", 0
	for _, i := range c.members {
		base := filepath.Base(masters[i].FileDir)
		g, seen := generic[base]
		if !seen {
			g = base == "." || base == "/" || classifier.IsGenericDirName(base)
			generic[base] = g
		}
		if g {
			continue
		}
		counts[base]++
		if counts[base] > bestN {
			best, bestN = base, counts[base]
		}
	}
	if best != "" {
		return best, SuggestionSourceFolder
	}

	return "", ""
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
