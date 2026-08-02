// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"fmt"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
)

// nameSuggestion is one rename candidate shown under the input.
type nameSuggestion struct {
	label  string // shown to the reviewer, e.g. "Springfield, Illinois"
	value  string // written as the folder name if picked
	detail string // right-hand hint, e.g. "~12km" or "used before"
}

// folderName turns a picked display name into the actual folder name — same
// sanitizing rule Confirm enforces on every node name.
func folderName(display string) string {
	// dropdown/search keep the comma ("Seoni, Himachal Pradesh") so a reviewer
	// can tell same-named places apart; SanitizeSegment strips it back out
	return vfs.SanitizeSegment(display)
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

	add := func(label, detail string) bool {
		value := folderName(label)
		if value == "" || seen[value] {
			return true
		}
		seen[value] = true
		m.suggestions = append(m.suggestions, nameSuggestion{label: label, value: value, detail: detail})
		return len(m.suggestions) < maxSuggestions
	}

	// 1. nearby places, already fetched — FullName because six rows reading
	// "Springfield" are not a choice, and what is picked is what names the folder
	for _, c := range m.geoCands {
		name := c.FullName
		if name == "" {
			name = c.Name
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(name), prefix) &&
			!strings.HasPrefix(strings.ToLower(c.Name), prefix) {
			continue
		}
		if !add(name, fmt.Sprintf("~%.0fkm", c.DistKM)) {
			return
		}
	}

	if prefix == "" {
		return // the two typed-prefix sources below have nothing to match on
	}

	// 2. names the reviewer confirmed before
	for _, l := range m.labels {
		if !strings.HasPrefix(strings.ToLower(l), prefix) {
			continue
		}
		if !add(l, "used before") {
			return
		}
	}

	// 3. geonames prefix search, so typing a city works on a folder with no GPS
	// to seed geoCands. One indexed LIKE 'x%' per keystroke, from 2 chars on.
	if len(prefix) >= minGeonamesPrefix && m.resolver != nil {
		if matches, err := m.resolver.SearchByName(m.ctx, typed, maxSuggestions); err == nil {
			for _, pm := range matches {
				if !add(pm.FullName, "") {
					return
				}
			}
		}
	}
}
