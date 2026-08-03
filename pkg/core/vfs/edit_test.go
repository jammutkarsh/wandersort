// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import "testing"

// siblingTree has two true siblings ("03", "09") under the same parent
// ("June") — the shape a real merge (two date-fallback clusters that turn out
// to be the same place) actually happens on.
func siblingTree() []Node {
	return []Node{{ID: "2024", Name: "2024", Children: []Node{
		{ID: "2024/June", Name: "June", Children: []Node{
			{ID: "2024/June/03", Name: "03", FileCount: 1},
			{ID: "2024/June/09", Name: "09", FileCount: 1},
		}},
	}}}
}

// crossBranchTree mirrors the real reported case: the same device's photos
// spread across three different months, each its own single-file leaf.
func crossBranchTree() []Node {
	leaf := func(id, name string) Node { return Node{ID: id, Name: name, FileCount: 1} }
	return []Node{{ID: "2017", Name: "2017", Children: []Node{
		{ID: "2017/April", Name: "April", Children: []Node{
			{ID: "2017/April/20", Name: "20", Children: []Node{
				leaf("2017/April/20/Canon EOS 700D", "Canon EOS 700D"),
			}},
		}},
		{ID: "2017/August", Name: "August", Children: []Node{
			{ID: "2017/August/15", Name: "15", Children: []Node{
				leaf("2017/August/15/Canon EOS 700D", "Canon EOS 700D"),
			}},
		}},
	}}}
}

// groupedTree is the reported shape: every month grouped by location and then
// by device, where the device (and location) level is the same everywhere and
// the reviewer doesn't want it.
func groupedTree() []Node {
	month := func(name string, n int) Node {
		return Node{ID: "2023/" + name, Name: name, FileCount: n, Children: []Node{
			{ID: "2023/" + name + "/Indore", Name: "Indore", FileCount: n, Children: []Node{
				{ID: "2023/" + name + "/Indore/Apple iPhone 13", Name: "Apple iPhone 13", FileCount: n},
			}},
		}}
	}
	return []Node{{ID: "2023", Name: "2023", FileCount: 13, Children: []Node{
		month("April", 10), month("August", 3),
	}}}
}

func TestMergeNodesSiblingsSucceeds(t *testing.T) {
	tree := siblingTree()

	newTree, mergedID, name, ancestor, err := MergeNodes(tree, []string{"2024/June/03", "2024/June/09"})
	if err != nil {
		t.Fatalf("MergeNodes: %v", err)
	}
	// both are plain unrenamed Date folders, so the merge proposes the day
	// range they span rather than keeping just the first pick's own name
	if mergedID != "2024/June/03" || name != "03_09" || ancestor != "June" {
		t.Fatalf("mergedID=%q name=%q ancestor=%q, want 2024/June/03, 03_09, June", mergedID, name, ancestor)
	}
	june := FindNode(newTree, "2024/June")
	if june == nil || len(june.Children) != 1 {
		t.Fatalf("June children = %+v, want exactly one merged node", june)
	}
	merged := june.Children[0]
	if merged.FileCount != 2 {
		t.Errorf("merged FileCount = %d, want 2 (both leaves' files)", merged.FileCount)
	}
	if len(merged.MergedIDs) != 1 || merged.MergedIDs[0] != "2024/June/09" {
		t.Errorf("MergedIDs = %v, want [2024/June/09] so Confirm remaps its files too", merged.MergedIDs)
	}
}

// TestMergeNodesCombinesDayRanges covers the reported case: merging several
// already-merged day ranges under one Month must propose the day range they
// jointly span ("01_02".."24_26" -> "01_26"), not just keep the first one's
// own name.
func TestMergeNodesCombinesDayRanges(t *testing.T) {
	names := []string{"01_02", "03", "04_08", "11_16", "18_22", "23", "24_26"}
	tree := []Node{{ID: "2024", Name: "2024", Children: []Node{
		{ID: "2024/June", Name: "June"},
	}}}
	ids := make([]string, len(names))
	for i, n := range names {
		id := "2024/June/" + n
		tree[0].Children[0].Children = append(tree[0].Children[0].Children, Node{ID: id, Name: n, FileCount: 1})
		ids[i] = id
	}

	newTree, mergedID, name, _, err := MergeNodes(tree, ids)
	if err != nil {
		t.Fatalf("MergeNodes: %v", err)
	}
	if name != "01_26" {
		t.Errorf("name = %q, want the combined day range %q", name, "01_26")
	}
	// baked directly into the node, not left for a caller's pending-rename map
	// to surface — see the [u]-undo bug this guards against in edit.go
	if survivor := FindNode(newTree, mergedID); survivor == nil || survivor.Name != "01_26" {
		t.Errorf("survivor.Name = %+v, want the combined range written onto the node itself", survivor)
	}
}

// TestMergeNodesSkipsDayRangeOutsideDateFolders covers the guard: a
// same-depth selection that isn't day-shaped (or mixes a renamed folder in)
// must fall back to ordinary first-pick naming, not misread an unrelated
// two-digit folder name as a day.
func TestMergeNodesSkipsDayRangeOutsideDateFolders(t *testing.T) {
	tree := []Node{{ID: "root", Name: "root", Children: []Node{
		{ID: "root/03", Name: "03", FileCount: 1},
		{ID: "root/Goa", Name: "Goa", FileCount: 1},
	}}}

	_, _, name, _, err := MergeNodes(tree, []string{"root/03", "root/Goa"})
	if err != nil {
		t.Fatalf("MergeNodes: %v", err)
	}
	if name != "03" {
		t.Errorf("name = %q, want the first pick's own name %q (not day-shaped as a pair)", name, "03")
	}
}

func TestMergeNodesAcrossBranchesCollapsesToOneNode(t *testing.T) {
	tree := crossBranchTree()

	newTree, _, _, _, err := MergeNodes(tree,
		[]string{"2017/April/20/Canon EOS 700D", "2017/August/15/Canon EOS 700D"})
	if err != nil {
		t.Fatalf("MergeNodes: %v", err)
	}

	year := FindNode(newTree, "2017")
	if year == nil || len(year.Children) != 1 {
		t.Fatalf("2017 has %d children, want 1 (every emptied Month chain pruned)", len(year.Children))
	}
	canon := year.Children[0]
	if canon.Name != "Canon EOS 700D" || canon.FileCount != 2 {
		t.Errorf("merged child = %q with %d files, want Canon EOS 700D with 2", canon.Name, canon.FileCount)
	}
	for _, month := range []string{"April", "August", "April/20"} {
		if n := FindNode(newTree, "2017/"+month); n != nil {
			t.Errorf("%s should have been pruned — nothing left under it", month)
		}
	}
}

func TestMergeNodesRejectsWithNoCommonAncestor(t *testing.T) {
	tree := []Node{
		{ID: "2017", Name: "2017", Children: []Node{{ID: "2017/Camera", Name: "Camera", FileCount: 1}}},
		{ID: "2018", Name: "2018", Children: []Node{{ID: "2018/Camera", Name: "Camera", FileCount: 1}}},
	}

	_, _, _, _, err := MergeNodes(tree, []string{"2017/Camera", "2018/Camera"})
	if err == nil {
		t.Fatal("expected rejection for leaves with no common ancestor")
	}
}

func TestMergeNodesRejectsFewerThanTwo(t *testing.T) {
	if _, _, _, _, err := MergeNodes(siblingTree(), []string{"2024/June/03"}); err == nil {
		t.Fatal("expected rejection for a single ID")
	}
}

// TestMergeNodesKeepsAnchorRename covers the naming rule: a reviewer's rename
// is written straight onto the node, so the merge keeps it rather than falling
// back to the day-range name it would propose for two plain Date folders.
func TestMergeNodesKeepsAnchorRename(t *testing.T) {
	tree := siblingTree()
	FindNode(tree, "2024/June/03").Name = "Renamed"

	_, _, name, _, err := MergeNodes(tree, []string{"2024/June/03", "2024/June/09"})
	if err != nil {
		t.Fatalf("MergeNodes: %v", err)
	}
	if name != "Renamed" {
		t.Errorf("name = %q, want the rename on the first pick, not its own name", name)
	}
}

func TestFlattenNodesCollapsesEverythingBelow(t *testing.T) {
	tree := groupedTree()

	newTree, absorbed, names, err := FlattenNodes(tree, []string{"2023/April"})
	if err != nil {
		t.Fatalf("FlattenNodes: %v", err)
	}
	if absorbed != 2 || len(names) != 1 || names[0] != "April" {
		t.Fatalf("absorbed=%d names=%v, want 2 descendants absorbed into %q", absorbed, names, "April")
	}

	april := FindNode(newTree, "2023/April")
	if april == nil || len(april.Children) != 0 {
		t.Fatalf("April = %+v, want a childless node", april)
	}
	if april.FileCount != 10 {
		t.Errorf("April FileCount = %d, want 10 (unchanged — it already counted the subtree)", april.FileCount)
	}
	want := map[string]bool{"2023/April/Indore": false, "2023/April/Indore/Apple iPhone 13": false}
	for _, id := range april.MergedIDs {
		if _, ok := want[id]; !ok {
			t.Errorf("unexpected MergedID %q", id)
		}
		want[id] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("MergedIDs = %v, missing %q", april.MergedIDs, id)
		}
	}
	if aug := FindNode(newTree, "2023/August/Indore/Apple iPhone 13"); aug == nil {
		t.Error("August's subtree should be untouched by a flatten on April")
	}
}

func TestFlattenNodesRejectsALeaf(t *testing.T) {
	tree := groupedTree()

	if _, _, _, err := FlattenNodes(tree, []string{"2023/April/Indore/Apple iPhone 13"}); err == nil {
		t.Fatal("expected a rejection flattening a leaf")
	}
}

func TestDropNodesLiftsChildren(t *testing.T) {
	tree := groupedTree()

	newTree, names, err := DropNodes(tree, []string{"2023/April/Indore"})
	if err != nil {
		t.Fatalf("DropNodes: %v", err)
	}
	if len(names) != 1 || names[0] != "Indore" {
		t.Fatalf("names = %v, want [Indore]", names)
	}

	april := FindNode(newTree, "2023/April")
	if len(april.Children) != 1 || april.Children[0].Name != "Apple iPhone 13" {
		t.Fatalf("April children = %+v, want the lifted device node", april.Children)
	}
	if got := april.MergedIDs; len(got) != 1 || got[0] != "2023/April/Indore" {
		t.Errorf("MergedIDs = %v, want just the dropped folder", got)
	}
	if FindNode(newTree, "2023/August/Indore") == nil {
		t.Error("dropping April's Indore should leave August's untouched")
	}
}

func TestDropNodesRejectsTopLevel(t *testing.T) {
	tree := groupedTree()

	newTree, _, err := DropNodes(tree, []string{"2023"})
	if err == nil {
		t.Fatal("expected rejection dropping a top-level folder")
	}
	if len(newTree[0].Children) != 2 {
		t.Errorf("tree changed on a rejected drop: %d children, want 2", len(newTree[0].Children))
	}
}

func TestSortTreeOrdersSplicedChildren(t *testing.T) {
	tree := []Node{{ID: "2024", Name: "2024", Children: []Node{
		{ID: "2024/June", Name: "June"},
		{ID: "2024/April", Name: "April"},
	}}}

	SortTree(tree)

	if tree[0].Children[0].Name != "April" || tree[0].Children[1].Name != "June" {
		t.Errorf("children = %v, want name order", tree[0].Children)
	}
}

func TestCloneTreeIsIndependentOfTheOriginal(t *testing.T) {
	tree := siblingTree()
	clone := CloneTree(tree)

	tree[0].Children[0].Children[0].FileCount = 99
	tree[0].Children[0].Children[0].MergedIDs = append(tree[0].Children[0].Children[0].MergedIDs, "x")

	if clone[0].Children[0].Children[0].FileCount == 99 {
		t.Error("mutating the original mutated the clone's FileCount")
	}
	if len(clone[0].Children[0].Children[0].MergedIDs) != 0 {
		t.Error("mutating the original mutated the clone's MergedIDs")
	}
}
