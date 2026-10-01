package vfs

// route places a new file in the deepest placed folder it matches completely
// (descend only where a complete match exists below). With no match, the file
// keeps its rule-built path. It only picks the path; persist still gives the
// file its own folder chain.

import (
	"maps"
	"slices"
)

// levelBit is one level a file states a value for, or a folder constrains.
type levelBit uint16

const (
	bitYear levelBit = 1 << iota
	bitMonth
	bitDate
	bitLocation
	bitDevice
	bitOrientation
	bitMedia
	// the folders that constrain nothing but still only take their own kind
	bitScreenshots
	bitFallback
	bitOrphan
)

// specialLevels are the folders whose bounds are [{}]: a file belongs in one
// only if the rules would have put it in one.
var specialLevels = map[string]levelBit{
	LevelScreenshots: bitScreenshots,
	LevelFallback:    bitFallback,
	LevelOrphan:      bitOrphan,
}

// placedTree is the placed folders, indexed for route. Read-only once built,
// so route runs on every assignTargetPaths worker at once.
type placedTree struct {
	rows  map[int64]folderRow
	kids  map[int64][]int64 // parent (0 = top) → children, in id order
	paths map[int64]string
}

func newPlacedTree(rows []folderRow) *placedTree {
	t := &placedTree{rows: map[int64]folderRow{}, kids: map[int64][]int64{}, paths: map[int64]string{}}
	for _, r := range rows {
		t.rows[r.ID] = r
	}
	// id order: the oldest of two matching folders wins, run after run
	for _, id := range slices.Sorted(maps.Keys(t.rows)) {
		p := t.rows[id].Parent
		t.kids[p] = append(t.kids[p], id)
		folderPath(t.rows, t.paths, id)
	}
	return t
}

// route returns the path of the placed folder m matches completely, and
// points m's levels and bounds at that folder's chain. ok is false when no
// placed folder holds everything m's own path says.
func (t *placedTree) route(m *masterFile) (dir string, ok bool) {
	if t == nil || len(t.rows) == 0 {
		return "", false
	}
	file, want := statement(m)
	id := t.find(0, file, want, 0)
	if id == 0 {
		return "", false
	}
	dir = t.paths[id]
	var levels []string
	var bounds []Bounds
	for ; id != 0; id = t.rows[id].Parent {
		levels = append([]string{t.rows[id].Level}, levels...)
		bounds = append([]Bounds{t.rows[id].Bounds}, bounds...)
	}
	m.dirLevels, m.dirBounds = levels, bounds
	return dir, true
}

// find is the deepest folder below parent that m matches completely: the
// first child (in id order) with a complete match beneath it, else parent
// itself if the chain down to it covers every level m states, else 0.
func (t *placedTree) find(parent int64, file Constraint, want, have levelBit) int64 {
	for _, id := range t.kids[parent] {
		covers, ok := t.matches(t.rows[id], file, want)
		if !ok {
			continue
		}
		if found := t.find(id, file, want, have|covers); found != 0 {
			return found
		}
	}
	if parent != 0 && have&want == want {
		return parent
	}
	return 0
}

// matches reports whether file fits folder r, and which levels the
// alternatives it fits constrain.
func (t *placedTree) matches(r folderRow, file Constraint, want levelBit) (levelBit, bool) {
	var covers levelBit
	if bit, ok := specialLevels[r.Level]; ok {
		if want&bit == 0 {
			return 0, false
		}
		covers = bit
	}
	ok := false
	for _, c := range r.Bounds {
		if (Bounds{c}).Matches(file) {
			ok = true
			covers |= constrained(c)
		}
	}
	return covers, ok
}

// statement is what m's planned path says about it: each level's value from
// the first folder stating it, and which levels those are.
func statement(m *masterFile) (Constraint, levelBit) {
	var file Constraint
	var want levelBit
	for d, level := range m.dirLevels {
		if bit, ok := specialLevels[level]; ok {
			want |= bit
			continue
		}
		if d >= len(m.dirBounds) || len(m.dirBounds[d]) == 0 {
			continue
		}
		c := m.dirBounds[d][0]
		fn, ft := file.levels()
		cn, ct := c.levels()
		for i := range fn {
			fill(fn[i].set, *cn[i].set)
		}
		for i := range ft {
			fill(ft[i].set, *ct[i].set)
		}
	}
	return file, want | constrained(file)
}

// fill sets dst from src unless an earlier folder already stated it.
func fill[T any](dst *[]T, src []T) {
	if *dst == nil {
		*dst = src
	}
}

// constrained is the levels c sets.
func constrained(c Constraint) levelBit {
	var b levelBit
	nums, texts := c.levels()
	for _, l := range nums {
		if *l.set != nil {
			b |= l.bit
		}
	}
	for _, l := range texts {
		if *l.set != nil {
			b |= l.bit
		}
	}
	return b
}
