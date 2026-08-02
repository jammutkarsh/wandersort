// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"fmt"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/path"
)

// nameSuggestion is one rename candidate shown under the input.
type nameSuggestion struct {
	label  string // shown to the reviewer, e.g. "Springfield, Illinois"
	value  string // written as the folder name if picked
	detail string // right-hand hint, e.g. "~12km" or "used before"
}

// folderName sanitizes arbitrary typed or accepted text into an actual folder
// name — same rule Confirm enforces on every node name. A location suggestion
// already comes pre-sanitized as FolderName (pkg/location's job, not this
// package's); this is for the paths that don't go through pkg/location at
// all: what the reviewer typed by hand, or a plain SOURCE_FOLDER suggestion.
func folderName(text string) string {
	return path.SanitizeSegment(text)
}

// maxSuggestions caps the rename dropdown — it renders above the key help, so
// an unbounded list would push the tree off screen.
const maxSuggestions = 8

// geoCandidateFetch over-fetches nearby places: the list is filtered in memory
// as the reviewer types, so one fetch has to cover every prefix they might type.
const geoCandidateFetch = 64

// minGeonamesPrefix is how many characters must be typed before the per-
// keystroke geonames search runs; below it every query matches half the table.
const minGeonamesPrefix = 2

// loadGeoCandidates caches nearby places for the current row. Called by [r] and
// ctrl+e only — refreshSuggestions filters this list in memory per keystroke.
func (m *Model) loadGeoCandidates() {
	m.geoCands = nil
	row := m.rows[m.cursor]
	if m.resolver == nil || row.node.Lat == nil || row.node.Lon == nil {
		return
	}
	if cands, err := m.resolver.Candidates(m.ctx, *row.node.Lat, *row.node.Lon, m.radiusDelta, geoCandidateFetch); err == nil {
		m.geoCands = cands
	}
}

// refreshSuggestions repopulates the rename dropdown from three ranked sources:
// nearby places, names confirmed in earlier reviews, then the geonames database.
func (m *Model) refreshSuggestions() {
	m.suggestions = nil
	seen := map[string]bool{}
	typed := strings.TrimSpace(m.input)
	prefix := strings.ToLower(typed)

	// add takes label (what's shown, e.g. "Springfield, Illinois") and value
	// (what's written if picked — already the actual folder name, sanitized).
	// pkg/location computes both for a place: it already knows which
	// qualifier a name needs, so it also knows what's safe in a directory name.
	add := func(label, value, detail string) bool {
		if value == "" || seen[value] {
			return true
		}
		seen[value] = true
		m.suggestions = append(m.suggestions, nameSuggestion{label: label, value: value, detail: detail})
		return len(m.suggestions) < maxSuggestions
	}

	// 1. nearby places, already fetched — FullName because six rows reading
	// "Springfield" are not a choice, and the state/country are the only thing
	// that tells them apart in this list; FolderName is what actually gets
	// written, and only qualifies on a real DB collision
	for _, c := range m.geoCands {
		label, value := c.FullName, c.FolderName
		if label == "" {
			label = c.Name
		}
		if value == "" {
			value = c.Name
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(label), prefix) &&
			!strings.HasPrefix(strings.ToLower(c.Name), prefix) {
			continue
		}
		if !add(label, value, fmt.Sprintf("~%.0fkm", c.DistKM)) {
			return
		}
	}

	if prefix == "" {
		return // the two typed-prefix sources below have nothing to match on
	}

	// 2. names the reviewer confirmed before — already a final folder name,
	// nothing to disambiguate further, just re-sanitized for safety
	for _, l := range m.labels {
		if !strings.HasPrefix(strings.ToLower(l), prefix) {
			continue
		}
		if !add(l, folderName(l), "used before") {
			return
		}
	}

	// 3. geonames prefix search, so typing a city works on a folder with no GPS
	// to seed geoCands. One indexed LIKE 'x%' per keystroke, from 2 chars on.
	if len(prefix) >= minGeonamesPrefix && m.resolver != nil {
		if matches, err := m.resolver.SearchByName(m.ctx, typed, maxSuggestions); err == nil {
			for _, pm := range matches {
				if !add(pm.FullName, pm.FolderName, "") {
					return
				}
			}
		}
	}
}
