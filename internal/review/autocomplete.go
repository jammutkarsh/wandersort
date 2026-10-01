package review

import (
	"github.com/jammutkarsh/wandersort/pkg/location"
)

// geoCandidateFetch over-fetches nearby places: the list is filtered in memory
// per keystroke, so one fetch must cover every prefix.
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

// refreshSuggestions repopulates the rename dropdown via location.Suggest.
func (m *Model) refreshSuggestions() {
	m.suggestions = m.resolver.Suggest(m.ctx, location.SuggestQuery{
		Prefix: m.input,
		Nearby: m.geoCands,
		Prior:  m.labels,
	})
	m.suggCursor = -1
}
