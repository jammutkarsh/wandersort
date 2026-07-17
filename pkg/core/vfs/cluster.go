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
	"time"

	"github.com/jammutkarsh/wandersort/pkg/core/scorer"
)

// cluster groups masters whose capture times sit within the configured gap of
// each other — one cluster ≈ one real-world event
type cluster struct {
	members    []int // indices into masters
	start, end time.Time
}

// clusterAndSuggest is the unlocated-file design: time-gap clustering, GPS
// spillover inside a cluster, and ranked name suggestions for whatever stays
// unresolved. Located files keep their own city; clusters that resolve to the
// same city coalesce naturally because the city becomes the path segment
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

	// anchors are computed from directly-located files only, before any
	// spillover mutates locations
	anchors := anchorCities(masters, labels)

	clusterNum := 0
	for ci := range clusters {
		c := &clusters[ci]
		city := majorityCity(masters, c.members)

		var unlocated []int
		for _, i := range c.members {
			if masters[i].location == "" {
				unlocated = append(unlocated, i)
			}
		}
		if len(unlocated) == 0 {
			continue // every member located directly; nothing to decide
		}

		clusterNum++
		id := fmt.Sprintf("c%d", clusterNum)

		if city != "" {
			// GPS spillover: one located member names the whole event
			for _, i := range unlocated {
				masters[i].location = city
				masters[i].clusterID = id
				masters[i].suggestion = city
				masters[i].suggestionSource = SuggestionSpillover
			}
			continue
		}

		// fully unresolved: propose a dated event segment plus the best suggestion
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

// majorityCity returns the most common directly-resolved city among members,
// or "" when none of them has one
func majorityCity(masters []masterFile, members []int) string {
	counts := map[string]int{}
	best, bestN := "", 0
	for _, i := range members {
		city := masters[i].location
		if city == "" {
			continue
		}
		counts[city]++
		if counts[city] > bestN {
			best, bestN = city, counts[city]
		}
	}
	return best
}

// anchorCities returns the ranked default locations: confirmed anchor labels
// (home/work — possibly the same city) first, then the top-2 most frequent
// directly-resolved cities of this build
func anchorCities(masters []masterFile, labels []userLabel) []string {
	var anchors []string
	seen := map[string]bool{}
	add := func(city string) {
		if city != "" && !seen[city] {
			seen[city] = true
			anchors = append(anchors, city)
		}
	}

	for _, l := range labels {
		if l.Kind == "ANCHOR_HOME" || l.Kind == "ANCHOR_WORK" {
			add(l.Label)
		}
	}

	counts := map[string]int{}
	for i := range masters {
		if masters[i].location != "" {
			counts[masters[i].location]++
		}
	}
	for range 2 {
		best, bestN := "", 0
		for city, n := range counts {
			if !seen[city] && (n > bestN || (n == bestN && city < best)) {
				best, bestN = city, n
			}
		}
		add(best)
	}
	return anchors
}

// suggestFor ranks a name suggestion for a fully unresolved cluster:
// confirmed label overlapping in time → anchor city → the user's own
// (non-generic) source folder name.
// ponytail: GEO_CANDIDATE (ranked nearby cities) is deferred until
// location.Lookup exposes candidates — see the TODO in pkg/location
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

	// most common meaningful source folder among the cluster's members.
	// Only the immediate parent folder's name is judged — file_dir is
	// absolute now, and generic segments higher up (Users, Downloads) must
	// not disqualify a meaningful leaf folder
	counts := map[string]int{}
	best, bestN := "", 0
	for _, i := range c.members {
		base := filepath.Base(masters[i].FileDir)
		if base == "." || base == "/" || scorer.IsGenericDirName(base) {
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

// eventSegment renders the dated placeholder segment for an unresolved
// cluster, e.g. "Jun_03", "Jun_03-05", or "Jun_30-Jul_01"
func eventSegment(start, end time.Time) string {
	s := start.Format("Jan_02")
	switch {
	case start.Year() == end.Year() && start.YearDay() == end.YearDay():
		return s
	case start.Year() == end.Year() && start.Month() == end.Month():
		return s + "-" + end.Format("02")
	default:
		return s + "-" + end.Format("Jan_02")
	}
}
