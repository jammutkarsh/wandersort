// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"github.com/jammutkarsh/wandersort/pkg/location"
)

// geoCandidateFetch over-fetches nearby places: pkg/location filters the list
// in memory as the reviewer types, so one fetch has to cover every prefix they
// might type.
const geoCandidateFetch = 64

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

// fillSuggestion writes the picked completion into the rename input, same as
// the config wizard's [tab]/[enter] pick.
func (m *Model) fillSuggestion(i int) {
	m.input = m.suggestions[i].Value
	m.refreshSuggestions()
}

// refreshSuggestions repopulates the rename dropdown. Ranking, deduplication
// and folder-safe naming all belong to pkg/location — it owns what a place is
// called, in a list and on disk.
func (m *Model) refreshSuggestions() {
	m.suggestions = m.resolver.Suggest(m.ctx, location.SuggestQuery{
		Prefix: m.input,
		Nearby: m.geoCands,
		Prior:  m.labels,
	})
	m.suggCursor = -1
}
