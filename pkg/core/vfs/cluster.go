// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/config"
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
func clusterAndSuggest(masters []masterFile, labels []userLabel, gap time.Duration) {
	if gap <= 0 {
		gap = defaultClusterGap
	}

	order := make([]int, 0, len(masters))
	for i := range masters {
		order = append(order, i)
	}
	sort.SliceStable(order, func(a, b int) bool {
		return masters[order[a]].takenAt.Before(masters[order[b]].takenAt)
	})

	var clusters []cluster
	for _, idx := range order {
		t := masters[idx].takenAt
		if len(clusters) == 0 || t.Sub(clusters[len(clusters)-1].end) > gap {
			clusters = append(clusters, cluster{start: t, end: t})
		}
		c := &clusters[len(clusters)-1]
		c.members = append(c.members, idx)
		c.end = t
	}

	anchors := anchorCities(labels)

	clusterNum := 0
	for ci := range clusters {
		c := &clusters[ci]
		city, cityIsHomeWork := majorityCity(masters, c.members)

		var unlocated []int
		for _, i := range c.members {
			if masters[i].location == "" {
				unlocated = append(unlocated, i)
			}
		}
		if len(unlocated) == 0 {
			continue // every member already located (home/work included); nothing to decide
		}

		clusterNum++
		id := fmt.Sprintf("c%d", clusterNum)

		// spillover: one located member names the whole event. A GPS-less file
		// clustered with home/work photos inherits atHomeWork too — it's
		// almost certainly the same everyday place (an indoor shot with no
		// fix), not a location folder HomeWorkDateOnly is meant to suppress
		// for its neighbours but not for it.
		if city != "" {
			for _, i := range unlocated {
				masters[i].location = city
				masters[i].atHomeWork = cityIsHomeWork
				masters[i].clusterID = id
				masters[i].suggestion = city
				masters[i].suggestionSource = SuggestionSpillover
			}
			continue
		}

		// nothing located: fall back to a dated segment plus a name suggestion.
		// No member here is atHomeWork — that always carries a real location
		// now, which would have made city non-empty above.
		seg := eventSegment(c.start, c.end)
		sug, src := suggestFor(masters, c, labels, anchors)
		for _, i := range c.members {
			masters[i].clusterID = id
			masters[i].eventSegment = seg
			masters[i].suggestion = sug
			masters[i].suggestionSource = src
		}
	}
}

// majorityCity returns the most common directly-resolved city among members
// (or "" when none of them has one), plus whether any member holding that
// city got it via atHomeWork — a location string alone can't tell a folded
// home/work name from a plain resolved one, so this checks the field instead.
func majorityCity(masters []masterFile, members []int) (city string, atHomeWork bool) {
	counts := map[string]int{}
	home := map[string]bool{}
	best, bestN := "", 0
	for _, i := range members {
		city := masters[i].location
		if city == "" {
			continue
		}
		if masters[i].atHomeWork {
			home[city] = true
		}
		counts[city]++
		if counts[city] > bestN {
			best, bestN = city, counts[city]
		}
	}
	return best, home[best]
}

// anchorCities returns confirmed home/work labels in confirmation order, bare
// city only — labels are saved fully qualified ("Indore, Madhya Pradesh,
// India") so ResolveByName can round-trip them, but the state/country exists
// to disambiguate the picker, not to become a folder name.
// Deliberately no "most frequent city in the library" fallback: that named a
// place with no relationship to the cluster. A confirmed anchor is a user
// assertion; a frequency count is a guess.
func anchorCities(labels []userLabel) []string {
	var anchors []string
	seen := map[string]bool{}
	for _, l := range labels {
		if (l.Kind == config.SavedPlaceHome || l.Kind == config.SavedPlaceWork) && l.Label != "" {
			city, _, _ := strings.Cut(l.Label, ",")
			if !seen[city] {
				seen[city] = true
				anchors = append(anchors, city)
			}
		}
	}
	return anchors
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
	best, bestN := "", 0
	for _, i := range c.members {
		base := filepath.Base(masters[i].FileDir)
		if base == "." || base == "/" || classifier.IsGenericDirName(base) {
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
