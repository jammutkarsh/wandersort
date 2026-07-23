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

	anchors := anchorCities(labels)

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

// anchorCities returns confirmed anchor labels (home/work — possibly the same
// city), in confirmation order. Deliberately does NOT fall back to "the
// library's most frequent city" — that guessed a place with no temporal or
// spatial relationship to the cluster being named, which showed up as a
// single confusing suggestion smeared across the whole tree (e.g. a DSLR
// photo with no GPS anywhere nearby "suggested" whatever city happens to
// dominate the user's phone-photo library). A confirmed anchor is a real
// user assertion ("I live/work here"); an unconfirmed frequency count isn't.
func anchorCities(labels []userLabel) []string {
	var anchors []string
	seen := map[string]bool{}
	for _, l := range labels {
		if (l.Kind == "ANCHOR_HOME" || l.Kind == "ANCHOR_WORK") && l.Label != "" && !seen[l.Label] {
			seen[l.Label] = true
			anchors = append(anchors, l.Label)
		}
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
// cluster. It sits under the month folder, so days alone suffice — e.g. "03",
// "03-05" — and only a cross-month span keeps month names: "Jun_30-Jul_01".
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
