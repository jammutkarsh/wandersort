// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// This file holds the review tree's reshaping rules (merge/drop/flatten) as
// plain []Node functions with no TUI knowledge, testable by stating a tree
// and asserting the result. Each mutates in place — CloneTree first for undo.

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
			out[i].MergedIDs = append([]string(nil), n.MergedIDs...)
		}
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
func FindNode(nodes []Node, id string) *Node {
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
func parentOf(nodes []Node, id string) *Node {
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
func removeChildByID(parent *Node, id string) {
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

// commonPathPrefix returns the longest shared leading "/"-segment run between
// two node IDs (their proposed directory paths) — their LCA's ID. "" means
// no shared ancestor.
func commonPathPrefix(a, b string) string {
	as := strings.Split(a, "/")
	bs := strings.Split(b, "/")
	n := min(len(as), len(bs))
	var i int
	for i = 0; i < n; i++ {
		if as[i] != bs[i] {
			break
		}
	}
	return strings.Join(as[:i], "/")
}

// collectLeafIDs records every childless node's ID, used to tell a real leaf
// from an ancestor a merge emptied out.
func collectLeafIDs(nodes []Node, out map[string]bool) {
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
func pruneEmptied(nodes []Node, leafIDs map[string]bool) []Node {
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
	id     string
	parent *Node
	value  Node
}

// MergeNodes folds every node in ids into one, under their lowest common
// ancestor by path. Returns the surviving node's ID, its name, and the
// ancestor's name for the caller's status line.
func MergeNodes(tree []Node, ids []string) (newTree []Node, mergedID, name, ancestorName string, err error) {
	// Assumes no id in ids is an ancestor of another — the review TUI's
	// same-depth-only selection already guarantees that.
	if len(ids) < 2 {
		return tree, "", "", "", fmt.Errorf("select at least two folders at the same level to merge")
	}

	picks := make([]mergePick, 0, len(ids))
	for _, id := range ids {
		n := FindNode(tree, id)
		if n == nil {
			return tree, "", "", "", fmt.Errorf("internal error locating merge target %q", id)
		}
		picks = append(picks, mergePick{id: id, parent: parentOf(tree, id), value: *n})
	}

	lcaID := picks[0].id
	for _, p := range picks[1:] {
		lcaID = commonPathPrefix(lcaID, p.id)
	}
	if lcaID == "" {
		return tree, "", "", "", fmt.Errorf("selected folders share no common ancestor to merge under")
	}
	lca := FindNode(tree, lcaID)
	if lca == nil {
		return tree, "", "", "", fmt.Errorf("internal error locating merge destination")
	}

	// leaves *before* the splice — afterwards a childless node is either one of
	// these or an ancestor the merge emptied out
	leafIDs := map[string]bool{}
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
func DropNodes(tree []Node, ids []string) (newTree []Node, names []string, err error) {
	type drop struct {
		parentID string
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
			return tree, nil, fmt.Errorf("internal error locating drop target %q", id)
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
		parent.Children = append(parent.Children, d.node.Children...)
		// files sitting directly in the dropped node remap onto the parent
		parent.MergedIDs = append(parent.MergedIDs, append([]string{d.node.ID}, d.node.MergedIDs...)...)
		names = append(names, d.node.Name)
	}

	return tree, names, nil
}

// FlattenNodes collapses everything below each node in ids directly into it
// (`2023/April/Indore/Apple iPhone 13` flattened at April becomes
// `2023/April` holding all ten files). Returns the flattened nodes' names.
func FlattenNodes(tree []Node, ids []string) (newTree []Node, absorbed int, names []string, err error) {
	var targets []string
	for _, id := range ids {
		// childless ids are skipped rather than erroring individually
		if n := FindNode(tree, id); n != nil && len(n.Children) > 0 {
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
		node.Children = nil // FileCount is unchanged — it already counted the subtree
		names = append(names, node.Name)
	}

	return tree, absorbed, names, nil
}
