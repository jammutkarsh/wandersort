// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import "strings"

// Guides returns the box-drawing prefix for each row of a depth-ordered tree
// ("│  ├─ "), so a screen renders a hierarchy without re-deriving which rows
// are last children. depths[i] is row i's depth; depth 0 rows stand alone with
// no prefix, matching the review tree's roots.
func Guides(depths []int) []string {
	// A row is a last child when no later row has the same depth before one
	// of smaller depth appears — rows deeper than it are its own descendants
	// and don't affect its own last-child status.
	isLast := make([]bool, len(depths))
	for i, d := range depths {
		isLast[i] = true
		for j := i + 1; j < len(depths); j++ {
			if depths[j] <= d {
				isLast[i] = depths[j] != d
				break
			}
		}
	}

	out := make([]string, len(depths))
	// ancestorLast[k] is whether the most recent row seen at depth k+1 was a
	// last child — the guide's continuation column (blank) or bar ("│  ") at
	// that level.
	var ancestorLast []bool
	for i, d := range depths {
		if d == 0 {
			out[i] = ""
			ancestorLast = ancestorLast[:0]
			continue
		}
		if len(ancestorLast) > d-1 {
			ancestorLast = ancestorLast[:d-1]
		}
		var b strings.Builder
		for _, last := range ancestorLast {
			if last {
				b.WriteString("   ")
			} else {
				b.WriteString("│  ")
			}
		}
		if isLast[i] {
			b.WriteString("└─ ")
		} else {
			b.WriteString("├─ ")
		}
		out[i] = b.String()
		ancestorLast = append(ancestorLast, isLast[i])
	}
	return out
}
