// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// This file holds the review tree's reshaping rules (merge/drop/flatten) as
// plain []Node functions with no TUI knowledge, testable by stating a tree
// and asserting the result. Each mutates in place — CloneTree first for undo.

// Constraint is one way a file can belong in a folder (spec D13): an AND of
// levels. A nil level constrains nothing; an empty non-nil one matches
// nothing. Usually one level is set, but a drop pushes the dropped folder's
// constraint into its children, so one can carry several.
type Constraint struct {
	Year  []int `json:"year,omitzero"`
	Month []int `json:"month,omitzero"`
	// Date is the days of the month, a set: {3, 20} is the 3rd and the 20th,
	// never the days between.
	Date        []int    `json:"date,omitzero"`
	Location    []string `json:"location,omitzero"`
	Device      []string `json:"device,omitzero"`
	Orientation []string `json:"orientation,omitzero"`
	Media       []string `json:"media,omitzero"`
}

// Bounds is what one folder holds: a file belongs if it satisfies any one of
// its alternatives, and a folder's full range is its Bounds AND every
// ancestor's. Alternatives keep a merge exact — Goa on the 3rd merged with
// Manali on the 20th does not admit Goa on the 20th. No alternatives matches
// nothing; one empty Constraint matches everything.
//
// Never mutated in place — every operation returns fresh slices — so trees
// and their undo clones share Bounds freely.
type Bounds []Constraint

// Union holds every file either side holds — the merge rule (D14): the
// alternatives side by side.
func (b Bounds) Union(o Bounds) Bounds {
	out := slices.Clone(b)
	for _, c := range o {
		out = out.with(c)
	}
	return out
}

// Intersect holds only the files both sides hold — the drop rule (D14): each
// pair of alternatives intersected, the empty ones gone.
func (b Bounds) Intersect(o Bounds) Bounds {
	out := Bounds{}
	for _, x := range b {
		for _, y := range o {
			if c, ok := x.intersect(y); ok {
				out = out.with(c)
			}
		}
	}
	return out
}

// Matches reports whether a file, stated as a Constraint holding its own
// values, satisfies any alternative. A level the file has no value for fails
// every alternative that constrains it.
func (b Bounds) Matches(file Constraint) bool {
	return slices.ContainsFunc(b, func(c Constraint) bool {
		return admits(c.Year, file.Year) && admits(c.Month, file.Month) && admits(c.Date, file.Date) &&
			admits(c.Location, file.Location) && admits(c.Device, file.Device) &&
			admits(c.Orientation, file.Orientation) && admits(c.Media, file.Media)
	})
}

// with adds c, folding it into an alternative it differs from on one level
// at most — an exact rewrite ({3, Goa} or {20, Goa} is {3 or 20, Goa}) that
// keeps an ordinary folder at one alternative however many files it holds.
func (b Bounds) with(c Constraint) Bounds {
	for i, a := range b {
		if j, ok := a.join(c); ok {
			return slices.Delete(slices.Clone(b), i, i+1).with(j)
		}
	}
	return append(slices.Clone(b), c)
}

// join is a ∪ c as one alternative, when they differ on one level at most.
func (a Constraint) join(c Constraint) (Constraint, bool) {
	diff := 0
	for _, same := range []bool{
		sameSet(a.Year, c.Year), sameSet(a.Month, c.Month), sameSet(a.Date, c.Date),
		sameSet(a.Location, c.Location), sameSet(a.Device, c.Device),
		sameSet(a.Orientation, c.Orientation), sameSet(a.Media, c.Media),
	} {
		if !same {
			diff++
		}
	}
	if diff > 1 {
		return Constraint{}, false
	}
	return Constraint{
		Year:        unionSet(a.Year, c.Year),
		Month:       unionSet(a.Month, c.Month),
		Date:        unionSet(a.Date, c.Date),
		Location:    unionSet(a.Location, c.Location),
		Device:      unionSet(a.Device, c.Device),
		Orientation: unionSet(a.Orientation, c.Orientation),
		Media:       unionSet(a.Media, c.Media),
	}, true
}

// intersect is a ∩ c; ok is false when any level comes out empty.
func (a Constraint) intersect(c Constraint) (Constraint, bool) {
	out := Constraint{
		Year:        intersectSet(a.Year, c.Year),
		Month:       intersectSet(a.Month, c.Month),
		Date:        intersectSet(a.Date, c.Date),
		Location:    intersectSet(a.Location, c.Location),
		Device:      intersectSet(a.Device, c.Device),
		Orientation: intersectSet(a.Orientation, c.Orientation),
		Media:       intersectSet(a.Media, c.Media),
	}
	for _, n := range []int{
		emptyLen(out.Year), emptyLen(out.Month), emptyLen(out.Date), emptyLen(out.Location),
		emptyLen(out.Device), emptyLen(out.Orientation), emptyLen(out.Media),
	} {
		if n == 0 {
			return Constraint{}, false
		}
	}
	return out, true
}

// emptyLen is len(s), or -1 for an unconstrained (nil) level.
func emptyLen[T any](s []T) int {
	if s == nil {
		return -1
	}
	return len(s)
}

// sameSet compares two sorted sets, nil (unconstrained) only equal to nil.
func sameSet[T comparable](a, b []T) bool {
	return (a == nil) == (b == nil) && slices.Equal(a, b)
}

// unionSet is a ∪ b, sorted; unconstrained on either side stays unconstrained.
func unionSet[T cmp.Ordered](a, b []T) []T {
	if a == nil || b == nil {
		return nil
	}
	out := append(make([]T, 0, len(a)+len(b)), a...)
	out = append(out, b...)
	slices.Sort(out)
	return slices.Compact(out)
}

// intersectSet is a ∩ b in a's (sorted) order; non-nil when both are, so an
// empty result reads as "nothing", not "anything".
func intersectSet[T comparable](a, b []T) []T {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	out := []T{}
	for _, v := range a {
		if slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	return out
}

// admits reports whether a level constraint c holds the file's values f.
func admits[T comparable](c, f []T) bool {
	if c == nil {
		return true
	}
	if len(f) == 0 {
		return false
	}
	for _, v := range f {
		if !slices.Contains(c, v) {
			return false
		}
	}
	return true
}

// pushDown intersects every descendant of n with its ancestors up to n, so
// each folder in n's subtree means on its own what it meant under them. A
// merge calls it before moving a subtree under a looser parent: two day
// folders' same-named Canon children stay "Canon on the 3rd" and "Canon on
// the 20th" once both days are one folder.
func pushDown(n *Node) {
	for i := range n.Children {
		c := &n.Children[i]
		c.Bounds = c.Bounds.Intersect(n.Bounds)
		pushDown(c)
	}
}

// SortTree restores name order after a structural edit — BuildTree emits
// sorted levels, but a splice (merge, drop, flatten) appends, and a folder
// landing below its siblings instead of between them reads as "the edit
// deleted it".
func SortTree(nodes []Node) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	for i := range nodes {
		SortTree(nodes[i].Children)
	}
}

// CloneTree deep-copies a node tree so a caller's undo snapshot is unaffected
// by later in-place mutation. Trees are folders only, never files, so a clone
// is cheap.
func CloneTree(nodes []Node) []Node {
	if nodes == nil {
		return nil
	}
	out := make([]Node, len(nodes))
	for i, n := range nodes {
		out[i] = n
		out[i].Children = CloneTree(n.Children)
		if n.Samples != nil {
			out[i].Samples = append([]string(nil), n.Samples...)
		}
		if n.MergedIDs != nil {
			out[i].MergedIDs = append([]int64(nil), n.MergedIDs...)
		}
		out[i].Bounds = slices.Clone(n.Bounds)
		if n.Lat != nil {
			lat := *n.Lat
			out[i].Lat = &lat
		}
		if n.Lon != nil {
			lon := *n.Lon
			out[i].Lon = &lon
		}
	}
	return out
}

// FindNode searches the tree for the node with the given ID.
func FindNode(nodes []Node, id int64) *Node {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
		if found := FindNode(nodes[i].Children, id); found != nil {
			return found
		}
	}
	return nil
}

// parentOf finds the parent of the node with the given ID, or nil if id names
// a top-level node or isn't found at all.
func parentOf(nodes []Node, id int64) *Node {
	for i := range nodes {
		for _, c := range nodes[i].Children {
			if c.ID == id {
				return &nodes[i]
			}
		}
		if found := parentOf(nodes[i].Children, id); found != nil {
			return found
		}
	}
	return nil
}

// removeChildByID removes the child with the given ID from parent's
// Children, if present.
func removeChildByID(parent *Node, id int64) {
	for i, c := range parent.Children {
		if c.ID == id {
			parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)
			return
		}
	}
}

// childByName finds parent's child named name, if any.
func childByName(parent *Node, name string) *Node {
	for i := range parent.Children {
		if parent.Children[i].Name == name {
			return &parent.Children[i]
		}
	}
	return nil
}

// mergeInto folds src into dst, recursively merging same-named children
// rather than leaving them as duplicate siblings.
func mergeInto(dst *Node, src Node) {
	dst.FileCount += src.FileCount
	dst.Bounds = dst.Bounds.Union(src.Bounds)
	dst.Samples = append(dst.Samples, src.Samples...)
	dst.MergedIDs = append(dst.MergedIDs, src.ID)
	dst.MergedIDs = append(dst.MergedIDs, src.MergedIDs...)
	for _, c := range src.Children {
		if twin := childByName(dst, c.Name); twin != nil {
			mergeInto(twin, c)
			continue
		}
		dst.Children = append(dst.Children, c)
	}
}

// chainTo returns the IDs from the top level down to id, id included, or nil
// when id isn't in the tree.
func chainTo(nodes []Node, id int64) []int64 {
	for i := range nodes {
		if nodes[i].ID == id {
			return []int64{id}
		}
		if below := chainTo(nodes[i].Children, id); below != nil {
			return append([]int64{nodes[i].ID}, below...)
		}
	}
	return nil
}

// commonChain returns the longest shared leading run of two chains — their
// lowest common ancestor is its last element. Empty means no shared ancestor.
func commonChain(a, b []int64) []int64 {
	n := min(len(a), len(b))
	var i int
	for i = 0; i < n; i++ {
		if a[i] != b[i] {
			break
		}
	}
	return a[:i]
}

// collectLeafIDs records every childless node's ID, used to tell a real leaf
// from an ancestor a merge emptied out.
func collectLeafIDs(nodes []Node, out map[int64]bool) {
	for i := range nodes {
		if len(nodes[i].Children) == 0 {
			out[nodes[i].ID] = true
		}
		collectLeafIDs(nodes[i].Children, out)
	}
}

// pruneEmptied drops ancestors a merge left with no children and refreshes
// FileCount bottom-up. leafIDs is the pre-merge leaf set: anything childless
// outside it is an emptied ancestor, not a real leaf.
func pruneEmptied(nodes []Node, leafIDs map[int64]bool) []Node {
	out := nodes[:0]
	for i := range nodes {
		n := nodes[i]
		if len(n.Children) > 0 {
			kept := pruneEmptied(n.Children, leafIDs)
			if len(kept) == 0 {
				continue
			}
			total := 0
			for _, k := range kept {
				total += k.FileCount
			}
			n.Children, n.FileCount = kept, total
		} else if !leafIDs[n.ID] {
			continue
		}
		out = append(out, n)
	}
	return out
}

// parseDayRange reads a Date-level folder name back into the day(s) it spans
// — "03" (a single day, dirFor's plain time.Format("02")) or "01_02"
// (mergeSameLocationDays' merged run, "%02d_%02d"). Anything else, ok is
// false: this is a narrow match on those two exact shapes, not a general
// number parser.
func parseDayRange(name string) (lo, hi int, ok bool) {
	a, b, found := strings.Cut(name, "_")
	if !found {
		b = a
	}
	if len(a) != 2 || len(b) != 2 {
		return 0, 0, false
	}
	lo, errA := strconv.Atoi(a)
	hi, errB := strconv.Atoi(b)
	if errA != nil || errB != nil || lo < 1 || lo > 31 || hi < 1 || hi > 31 {
		return 0, 0, false
	}
	return lo, hi, true
}

// formatDayRange is mergeSameLocationDays' own format string, mirrored here
// so a merged Date folder's name matches what the pipeline would have
// proposed had it seen these days as one run to begin with.
func formatDayRange(lo, hi int) string {
	if lo == hi {
		return fmt.Sprintf("%02d", lo)
	}
	return fmt.Sprintf("%02d_%02d", lo, hi)
}

// combinedDayRange spans every pick's day range, if every pick is a plain
// Date-level folder ("03", "01_02"). Anything that isn't day-shaped (a
// location, a device, a folder the reviewer already renamed to something of
// their own) bails the whole thing out: this only fires when the selection is
// unambiguously a date merge.
func combinedDayRange(picks []mergePick) (lo, hi int, ok bool) {
	for i, p := range picks {
		d1, d2, valid := parseDayRange(p.value.Name)
		if !valid {
			return 0, 0, false
		}
		if i == 0 || d1 < lo {
			lo = d1
		}
		if i == 0 || d2 > hi {
			hi = d2
		}
	}
	return lo, hi, true
}

// mergePick is one selected row resolved to its node, its parent (for the
// splice), and the raw value the merge absorbs.
type mergePick struct {
	id     int64
	parent *Node
	value  Node
}

// MergeNodes folds every node in ids into one, under their lowest common
// ancestor. Returns the surviving node's ID, its name, and the
// ancestor's name for the caller's status line.
func MergeNodes(tree []Node, ids []int64) (newTree []Node, mergedID int64, name, ancestorName string, err error) {
	// Assumes no id in ids is an ancestor of another — the review TUI's
	// same-depth-only selection already guarantees that.
	if len(ids) < 2 {
		return tree, 0, "", "", fmt.Errorf("select at least two folders at the same level to merge")
	}

	picks := make([]mergePick, 0, len(ids))
	for _, id := range ids {
		n := FindNode(tree, id)
		if n == nil {
			return tree, 0, "", "", fmt.Errorf("internal error locating merge target %d", id)
		}
		if n.Fixed() {
			return tree, 0, "", "", ErrFixedFolder
		}
		picks = append(picks, mergePick{id: id, parent: parentOf(tree, id), value: *n})
	}

	// the picks' lowest common ancestor, found by walking the tree
	shared := chainTo(tree, picks[0].id)
	for _, p := range picks[1:] {
		shared = commonChain(shared, chainTo(tree, p.id))
	}
	if len(shared) == 0 {
		return tree, 0, "", "", fmt.Errorf("selected folders share no common ancestor to merge under")
	}
	lca := FindNode(tree, shared[len(shared)-1])
	if lca == nil {
		return tree, 0, "", "", fmt.Errorf("internal error locating merge destination")
	}

	// each pick is lifted out from under the folders between it and the LCA,
	// so it takes their bounds with it — "Canon under the 3rd" stays the 3rd —
	// and its subtree takes its own, since the merged parent will be looser
	for i := range picks {
		chain := chainTo(tree, picks[i].id)
		if len(chain) == len(shared) {
			// this pick is the LCA itself: another pick sits inside it
			return tree, 0, "", "", fmt.Errorf("can't merge a folder with a folder inside it")
		}
		for _, between := range chain[len(shared) : len(chain)-1] {
			picks[i].value.Bounds = picks[i].value.Bounds.Intersect(FindNode(tree, between).Bounds)
		}
		picks[i].value.Children = CloneTree(picks[i].value.Children)
		pushDown(&picks[i].value)
	}

	// leaves *before* the splice — afterwards a childless node is either one of
	// these or an ancestor the merge emptied out
	leafIDs := map[int64]bool{}
	collectLeafIDs(tree, leafIDs)

	// the first id's own name — already the reviewer's rename if they typed one,
	// since a rename is written straight onto the node
	target := picks[0].value.Name

	// unless every pick is a plain Date folder — merging days proposes the day
	// range they actually span, the same name the pipeline would have proposed
	// had it seen them as one run from the start
	if lo, hi, ok := combinedDayRange(picks); ok {
		target = formatDayRange(lo, hi)
	}

	// absorb the rest; mergeInto collapses same-named children recursively, so
	// three Goa days give one Goa holding one merged device folder
	merged := picks[0].value
	for _, p := range picks[1:] {
		mergeInto(&merged, p.value)
	}
	merged.Name = target

	// splice: the picks leave the tree entirely and reappear as one child of
	// the LCA. Their IDs ride along on MergedIDs so Confirm remaps their files.
	for _, p := range picks {
		if p.parent != nil {
			removeChildByID(p.parent, p.id)
		}
	}
	lca.Children = append(lca.Children, merged)
	tree = pruneEmptied(tree, leafIDs)

	return tree, merged.ID, target, lca.Name, nil
}

// DropNodes removes each node in ids, lifting its children onto its parent,
// one group-by level shallower. Returns the dropped nodes' names, in order.
//
// ponytail: a drop (and a flatten below) does not survive a re-plan that runs
// while some of its files are still untransferred — an execute stopped
// partway, then a scan. A rename or a merge does: the folder is still there
// holding the placed files, so route.go matches the leftovers into it by
// bounds. A dropped folder is gone, so there is nothing to match, and the
// re-plan proposes the level again for whatever has not moved yet: the
// library ends up with `03/A.HEIC` beside `03/Apple-iPhone-15-Pro/B.HEIC`.
// No file is lost and dropping again fixes it. The fix is to keep the user
// out of that order rather than to teach the planner to remember a removed
// folder: the app knows a copy did not finish and offers to finish it first
// (issue 21). Recording the removal on the surviving folder's bounds would
// also work and costs far more — don't reach for it unless the offer isn't
// enough.
func DropNodes(tree []Node, ids []int64) (newTree []Node, names []string, err error) {
	type drop struct {
		parentID int64
		node     Node
	}
	drops := make([]drop, 0, len(ids))
	for _, id := range ids {
		parent := parentOf(tree, id)
		if parent == nil {
			// a top-level node has no parent to lift its children onto — its
			// files would land in the library root ([D] flattens it instead)
			return tree, nil, fmt.Errorf("can't drop a top-level folder — its files would land in the library root ([D] flattens it instead)")
		}
		n := FindNode(tree, id)
		if n == nil {
			return tree, nil, fmt.Errorf("internal error locating drop target %d", id)
		}
		if n.Fixed() {
			return tree, nil, ErrFixedFolder
		}
		drops = append(drops, drop{parentID: parent.ID, node: *n})
	}
	if len(drops) == 0 {
		return tree, nil, nil
	}

	for _, d := range drops {
		parent := FindNode(tree, d.parentID)
		if parent == nil {
			continue
		}
		removeChildByID(parent, d.node.ID)
		// each lifted child keeps what the dropped folder constrained, so
		// dropping `12` over `Panji` leaves a Panji that still means day 12
		for _, c := range d.node.Children {
			c.Bounds = c.Bounds.Intersect(d.node.Bounds)
			parent.Children = append(parent.Children, c)
		}
		// files sitting directly in the dropped node remap onto the parent
		parent.MergedIDs = append(parent.MergedIDs, append([]int64{d.node.ID}, d.node.MergedIDs...)...)
		names = append(names, d.node.Name)
	}

	return tree, names, nil
}

// FlattenNodes collapses everything below each node in ids directly into it
// (`2023/April/Indore/Apple iPhone 13` flattened at April becomes
// `2023/April` holding all ten files). Returns the flattened nodes' names.
// Carries DropNodes' ponytail caveat: a re-plan over the leftovers of a
// stopped execute proposes the collapsed levels again.
func FlattenNodes(tree []Node, ids []int64) (newTree []Node, absorbed int, names []string, err error) {
	var targets []int64
	for _, id := range ids {
		n := FindNode(tree, id)
		// a flattened year would lose its months; a flattened month keeps itself
		if n != nil && n.Level == LevelYear {
			return tree, 0, nil, ErrFixedFolder
		}
		// childless ids are skipped rather than erroring individually
		if n != nil && len(n.Children) > 0 {
			targets = append(targets, id)
		}
	}
	if len(targets) == 0 {
		return tree, 0, nil, fmt.Errorf("nothing below the selected folder(s) to flatten")
	}

	for _, id := range targets {
		node := FindNode(tree, id)
		if node == nil {
			continue
		}
		// record every descendant so Confirm remaps their files onto this node
		var absorb func(children []Node)
		absorb = func(children []Node) {
			for _, c := range children {
				absorbed++
				node.MergedIDs = append(node.MergedIDs, c.ID)
				node.MergedIDs = append(node.MergedIDs, c.MergedIDs...)
				absorb(c.Children)
			}
		}
		absorb(node.Children)
		// FileCount is unchanged — it already counted the subtree — and so are
		// the node's own Bounds: the descendants' constraints go with them
		node.Children = nil
		names = append(names, node.Name)
	}

	return tree, absorbed, names, nil
}
