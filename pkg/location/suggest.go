// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package location

import (
	"context"
	"fmt"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/path"
)

// Suggestion is one completion offered while a folder is being renamed.
// Label and Value come from different fields on purpose: browsing a list of
// candidates and deciding what a folder should be called are different
// questions. Conflating them either strips the context that tells six
// Springfields apart, or over-qualifies a name nothing collides with.
type Suggestion struct {
	Label  string // shown to the user, e.g. "Springfield, Illinois, United States"
	Value  string // written as the folder name if picked — already sanitized
	Detail string // right-hand hint, e.g. "~12km" or "used before"
}

// SuggestQuery is what a rename picker knows when it asks for completions.
type SuggestQuery struct {
	// Prefix is what has been typed so far. Empty offers Nearby only — the
	// other two sources have nothing to match on.
	Prefix string
	// Nearby are places around the folder's own GPS, from Candidates. The
	// caller fetches them because it decides when the search radius changes;
	// they are filtered here per keystroke without touching the database.
	Nearby []Candidate
	// Prior are names the user has typed before, offered as "used before".
	Prior []string
	// Limit caps the list. 0 means DefaultSuggestLimit.
	Limit int
}

// DefaultSuggestLimit caps a rename dropdown at what fits on screen above the
// key help without pushing the tree off it.
const DefaultSuggestLimit = 8

// minPrefixSearch is how many characters must be typed before the per-keystroke
// prefix search runs; below it every query matches half the table.
const minPrefixSearch = 2

// Suggest ranks rename completions from three sources: places near the folder,
// names the user has used before, then a prefix search of the geonames
// database. Ranked in that order because the nearest sources are the most
// likely answers, and deduplicated on the folder name — two entries writing
// the same directory are not a choice.
//
// This package owns the ranking because it owns the names: it already computes
// which qualifier a place needs (DisplayName), how it reads in a list
// (FullName), and what is safe to write to disk (FolderName). A caller
// assembling those itself has to re-derive all three rules.
func (r *Resolver) Suggest(ctx context.Context, q SuggestQuery) []Suggestion {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultSuggestLimit
	}

	var out []Suggestion
	seen := map[string]bool{}
	// add reports whether there is room for more.
	add := func(label, value, detail string) bool {
		if value == "" || seen[value] {
			return true
		}
		seen[value] = true
		out = append(out, Suggestion{Label: label, Value: value, Detail: detail})
		return len(out) < limit
	}

	typed := strings.TrimSpace(q.Prefix)
	prefix := strings.ToLower(typed)

	for _, c := range q.Nearby {
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
			return out
		}
	}

	if prefix == "" {
		return out // the two typed-prefix sources below have nothing to match on
	}

	// already a final folder name; re-sanitized because it is arbitrary text
	// the user typed, not something this package produced
	for _, name := range q.Prior {
		if !strings.HasPrefix(strings.ToLower(name), prefix) {
			continue
		}
		if !add(name, path.SanitizeSegment(name), "used before") {
			return out
		}
	}

	// so typing a city works on a folder with no GPS to seed Nearby.
	// One indexed LIKE 'x%' per keystroke, from minPrefixSearch chars on.
	if r == nil || len(prefix) < minPrefixSearch {
		return out
	}
	matches, err := r.SearchByName(ctx, typed, limit)
	if err != nil {
		return out
	}
	for _, m := range matches {
		if !add(m.FullName, m.FolderName, "") {
			return out
		}
	}
	return out
}
