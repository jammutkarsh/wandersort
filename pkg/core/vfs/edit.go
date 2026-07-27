// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"fmt"
	"sort"
	"strings"
)

// This file holds the review tree's reshaping rules — merge, drop, flatten —
// and the tree-walking helpers they share with Confirm's MergedIDs handling.
// They operate on plain []Node/*Node with no knowledge of a TUI: a caller
// (internal/review's Model) resolves a keypress and a row selection into a
// []Node.ID list, calls one of these, and applies the result back onto its
// own cursor/undo/status state. That split is what lets a tree edit be tested
// by stating a tree and asserting the result, without going through
// tea.KeyMsg.
//
// Every function here mutates its tree argument in place (via pointers
// obtained through FindNode) and also returns it, so a caller must snapshot
// (CloneTree) before calling if it wants an undo step — same as Confirm's
// MergedIDs invariant, this package owns the whole thing, not just half.
// Each fails only before any mutation happens, so an error return always
// leaves the input tree untouched.

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
		if n.Suggestions != nil {
			out[i].Suggestions = append([]Suggestion(nil), n.Suggestions...)
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

// finalName is the segment a node will be written as: the caller's pending
// rename if any, else the proposed name. Suggestions count only once accepted.
func finalName(n Node, pending map[string]string) string {
	if name, ok := pending[n.ID]; ok {
		return name
	}
	return n.Name
}

// childByName finds parent's child that will end up named name, if any.
func childByName(parent *Node, name string, pending map[string]string) *Node {
	for i := range parent.Children {
		if finalName(parent.Children[i], pending) == name {
			return &parent.Children[i]
		}
	}
	return nil
}

// mergeInto folds src into dst, recursively merging same-named children
// rather than leaving them as duplicate siblings.
func mergeInto(dst *Node, src Node, pending map[string]string) {
	dst.FileCount += src.FileCount
	dst.Samples = append(dst.Samples, src.Samples...)
	dst.MergedIDs = append(dst.MergedIDs, src.ID)
	dst.MergedIDs = append(dst.MergedIDs, src.MergedIDs...)
	for _, c := range src.Children {
		if twin := childByName(dst, finalName(c, pending), pending); twin != nil {
			mergeInto(twin, c, pending)
			continue
		}
		dst.Children = append(dst.Children, c)
	}
}

// commonPathPrefix returns the longest shared leading run of "/"-separated
// segments between two node IDs (which are literally their proposed
// directory paths) — i.e. their lowest common ancestor's ID. "" means no
// shared ancestor at all (e.g. different years).
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

// MergeNodes folds every node in ids into one, under their lowest common
// ancestor by path, and returns the tree with that edit applied plus the
// surviving node's ID, its name (the first id's own name, or the pending
// rename on it — never a suggestion, which is an offer nobody accepted), and
// the ancestor's name for the caller's status line.
//
// ids must name at least two nodes; a caller with a single-row selection
// should reject that itself rather than rely on this error. Assumes no id in
// ids is an ancestor of another — a caller like the review TUI's
// same-depth-only selection already guarantees that.
func MergeNodes(tree []Node, ids []string, pending map[string]string) (newTree []Node, mergedID, name, ancestorName string, err error) {
	if len(ids) < 2 {
		return tree, "", "", "", fmt.Errorf("select at least two folders at the same level to merge")
	}

	type pick struct {
		id     string
		parent *Node
		value  Node
	}
	picks := make([]pick, 0, len(ids))
	for _, id := range ids {
		n := FindNode(tree, id)
		if n == nil {
			return tree, "", "", "", fmt.Errorf("internal error locating merge target %q", id)
		}
		picks = append(picks, pick{id: id, parent: parentOf(tree, id), value: *n})
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

	target := finalName(picks[0].value, pending)

	// absorb the rest; mergeInto collapses same-named children recursively, so
	// three Goa days give one Goa holding one merged device folder
	merged := picks[0].value
	for _, p := range picks[1:] {
		mergeInto(&merged, p.value, pending)
	}

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

// DropNodes removes each node in ids, lifting its children onto its parent —
// dropping "Apple iPhone 13" then "Indore" under 2023/April leaves April
// holding the files, one group-by level shallower. Files sitting directly in
// a dropped node remap onto the parent via MergedIDs.
//
// Fails, tree unchanged, if any id names a top-level node — its files would
// land in the library root. Returns the dropped nodes' names, in ids order.
// Assumes no id in ids is an ancestor of another.
func DropNodes(tree []Node, ids []string) (newTree []Node, names []string, err error) {
	type drop struct {
		parentID string
		node     Node
	}
	drops := make([]drop, 0, len(ids))
	for _, id := range ids {
		parent := parentOf(tree, id)
		if parent == nil {
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
		// files sitting directly in the dropped folder remap onto the parent
		parent.MergedIDs = append(parent.MergedIDs, append([]string{d.node.ID}, d.node.MergedIDs...)...)
		names = append(names, d.node.Name)
	}

	return tree, names, nil
}

// FlattenNodes collapses everything below each node in ids directly into it:
// `2023/April/Indore/Apple iPhone 13` flattened at April becomes
// `2023/April` holding all ten files. FileCount is unchanged (it already
// counts the subtree); every former descendant remaps onto the node via
// MergedIDs.
//
// ids with no children are skipped; if none remain, returns an error and the
// tree unchanged. Returns the flattened nodes' names, in ids order. Assumes
// no id in ids is an ancestor of another.
func FlattenNodes(tree []Node, ids []string) (newTree []Node, absorbed int, names []string, err error) {
	var targets []string
	for _, id := range ids {
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
		node.Children = nil
		names = append(names, node.Name)
	}

	return tree, absorbed, names, nil
}
