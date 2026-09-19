// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"reflect"
	"testing"
)

// pathIDs gives every folder path a test states a stable int ID, so a
// hand-built tree still reads as paths.
var pathIDs = map[string]int64{}

func pid(path string) int64 {
	if id, ok := pathIDs[path]; ok {
		return id
	}
	id := int64(len(pathIDs) + 1)
	pathIDs[path] = id
	return id
}

func pids(paths ...string) []int64 {
	ids := make([]int64, len(paths))
	for i, p := range paths {
		ids[i] = pid(p)
	}
	return ids
}

// siblingTree has two true siblings ("03", "09") under the same parent
// ("June") — the shape a real merge (two date-fallback clusters that turn out
// to be the same place) actually happens on.
func siblingTree() []Node {
	return []Node{{ID: pid("2024"), Name: "2024", Children: []Node{
		{ID: pid("2024/June"), Name: "June", Children: []Node{
			{ID: pid("2024/June/03"), Name: "03", FileCount: 1},
			{ID: pid("2024/June/09"), Name: "09", FileCount: 1},
		}},
	}}}
}

// crossBranchTree mirrors the real reported case: the same device's photos
// spread across three different months, each its own single-file leaf.
func crossBranchTree() []Node {
	leaf := func(id, name string) Node { return Node{ID: pid(id), Name: name, FileCount: 1} }
	return []Node{{ID: pid("2017"), Name: "2017", Children: []Node{
		{ID: pid("2017/April"), Name: "April", Children: []Node{
			{ID: pid("2017/April/20"), Name: "20", Children: []Node{
				leaf("2017/April/20/Canon EOS 700D", "Canon EOS 700D"),
			}},
		}},
		{ID: pid("2017/August"), Name: "August", Children: []Node{
			{ID: pid("2017/August/15"), Name: "15", Children: []Node{
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
		return Node{ID: pid("2023/" + name), Name: name, FileCount: n, Children: []Node{
			{ID: pid("2023/" + name + "/Indore"), Name: "Indore", FileCount: n, Children: []Node{
				{ID: pid("2023/" + name + "/Indore/Apple iPhone 13"), Name: "Apple iPhone 13", FileCount: n},
			}},
		}}
	}
	return []Node{{ID: pid("2023"), Name: "2023", FileCount: 13, Children: []Node{
		month("April", 10), month("August", 3),
	}}}
}

func TestMergeNodesSiblingsSucceeds(t *testing.T) {
	tree := siblingTree()

	newTree, mergedID, name, ancestor, err := MergeNodes(tree, pids("2024/June/03", "2024/June/09"))
	if err != nil {
		t.Fatalf("MergeNodes: %v", err)
	}
	// both are plain unrenamed Date folders, so the merge proposes the day
	// range they span rather than keeping just the first pick's own name
	if mergedID != pid("2024/June/03") || name != "03_09" || ancestor != "June" {
		t.Fatalf("mergedID=%d name=%q ancestor=%q, want 2024/June/03, 03_09, June", mergedID, name, ancestor)
	}
	june := FindNode(newTree, pid("2024/June"))
	if june == nil || len(june.Children) != 1 {
		t.Fatalf("June children = %+v, want exactly one merged node", june)
	}
	merged := june.Children[0]
	if merged.FileCount != 2 {
		t.Errorf("merged FileCount = %d, want 2 (both leaves' files)", merged.FileCount)
	}
	if len(merged.MergedIDs) != 1 || merged.MergedIDs[0] != pid("2024/June/09") {
		t.Errorf("MergedIDs = %v, want [2024/June/09] so Confirm remaps its files too", merged.MergedIDs)
	}
}

// TestMergeNodesCombinesDayRanges covers the reported case: merging several
// already-merged day ranges under one Month must propose the day range they
// jointly span ("01_02".."24_26" -> "01_26"), not just keep the first one's
// own name.
func TestMergeNodesCombinesDayRanges(t *testing.T) {
	names := []string{"01_02", "03", "04_08", "11_16", "18_22", "23", "24_26"}
	tree := []Node{{ID: pid("2024"), Name: "2024", Children: []Node{
		{ID: pid("2024/June"), Name: "June"},
	}}}
	ids := make([]int64, len(names))
	for i, n := range names {
		id := "2024/June/" + n
		tree[0].Children[0].Children = append(tree[0].Children[0].Children, Node{ID: pid(id), Name: n, FileCount: 1})
		ids[i] = pid(id)
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
	tree := []Node{{ID: pid("root"), Name: "root", Children: []Node{
		{ID: pid("root/03"), Name: "03", FileCount: 1},
		{ID: pid("root/Goa"), Name: "Goa", FileCount: 1},
	}}}

	_, _, name, _, err := MergeNodes(tree, pids("root/03", "root/Goa"))
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
		pids("2017/April/20/Canon EOS 700D", "2017/August/15/Canon EOS 700D"))
	if err != nil {
		t.Fatalf("MergeNodes: %v", err)
	}

	year := FindNode(newTree, pid("2017"))
	if year == nil || len(year.Children) != 1 {
		t.Fatalf("2017 has %d children, want 1 (every emptied Month chain pruned)", len(year.Children))
	}
	canon := year.Children[0]
	if canon.Name != "Canon EOS 700D" || canon.FileCount != 2 {
		t.Errorf("merged child = %q with %d files, want Canon EOS 700D with 2", canon.Name, canon.FileCount)
	}
	for _, month := range []string{"April", "August", "April/20"} {
		if n := FindNode(newTree, pid("2017/"+month)); n != nil {
			t.Errorf("%s should have been pruned — nothing left under it", month)
		}
	}
}

func TestMergeNodesRejectsWithNoCommonAncestor(t *testing.T) {
	tree := []Node{
		{ID: pid("2017"), Name: "2017", Children: []Node{{ID: pid("2017/Camera"), Name: "Camera", FileCount: 1}}},
		{ID: pid("2018"), Name: "2018", Children: []Node{{ID: pid("2018/Camera"), Name: "Camera", FileCount: 1}}},
	}

	_, _, _, _, err := MergeNodes(tree, pids("2017/Camera", "2018/Camera"))
	if err == nil {
		t.Fatal("expected rejection for leaves with no common ancestor")
	}
}

func TestMergeNodesRejectsFewerThanTwo(t *testing.T) {
	if _, _, _, _, err := MergeNodes(siblingTree(), pids("2024/June/03")); err == nil {
		t.Fatal("expected rejection for a single ID")
	}
}

// TestMergeNodesKeepsAnchorRename covers the naming rule: a reviewer's rename
// is written straight onto the node, so the merge keeps it rather than falling
// back to the day-range name it would propose for two plain Date folders.
func TestMergeNodesKeepsAnchorRename(t *testing.T) {
	tree := siblingTree()
	FindNode(tree, pid("2024/June/03")).Name = "Renamed"

	_, _, name, _, err := MergeNodes(tree, pids("2024/June/03", "2024/June/09"))
	if err != nil {
		t.Fatalf("MergeNodes: %v", err)
	}
	if name != "Renamed" {
		t.Errorf("name = %q, want the rename on the first pick, not its own name", name)
	}
}

func TestFlattenNodesCollapsesEverythingBelow(t *testing.T) {
	tree := groupedTree()

	newTree, absorbed, names, err := FlattenNodes(tree, pids("2023/April"))
	if err != nil {
		t.Fatalf("FlattenNodes: %v", err)
	}
	if absorbed != 2 || len(names) != 1 || names[0] != "April" {
		t.Fatalf("absorbed=%d names=%v, want 2 descendants absorbed into %q", absorbed, names, "April")
	}

	april := FindNode(newTree, pid("2023/April"))
	if april == nil || len(april.Children) != 0 {
		t.Fatalf("April = %+v, want a childless node", april)
	}
	if april.FileCount != 10 {
		t.Errorf("April FileCount = %d, want 10 (unchanged — it already counted the subtree)", april.FileCount)
	}
	want := map[int64]bool{pid("2023/April/Indore"): false, pid("2023/April/Indore/Apple iPhone 13"): false}
	for _, id := range april.MergedIDs {
		if _, ok := want[id]; !ok {
			t.Errorf("unexpected MergedID %d", id)
		}
		want[id] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("MergedIDs = %v, missing %d", april.MergedIDs, id)
		}
	}
	if aug := FindNode(newTree, pid("2023/August/Indore/Apple iPhone 13")); aug == nil {
		t.Error("August's subtree should be untouched by a flatten on April")
	}
}

func TestFlattenNodesRejectsALeaf(t *testing.T) {
	tree := groupedTree()

	if _, _, _, err := FlattenNodes(tree, pids("2023/April/Indore/Apple iPhone 13")); err == nil {
		t.Fatal("expected a rejection flattening a leaf")
	}
}

func TestDropNodesLiftsChildren(t *testing.T) {
	tree := groupedTree()

	newTree, names, err := DropNodes(tree, pids("2023/April/Indore"))
	if err != nil {
		t.Fatalf("DropNodes: %v", err)
	}
	if len(names) != 1 || names[0] != "Indore" {
		t.Fatalf("names = %v, want [Indore]", names)
	}

	april := FindNode(newTree, pid("2023/April"))
	if len(april.Children) != 1 || april.Children[0].Name != "Apple iPhone 13" {
		t.Fatalf("April children = %+v, want the lifted device node", april.Children)
	}
	if got := april.MergedIDs; len(got) != 1 || got[0] != pid("2023/April/Indore") {
		t.Errorf("MergedIDs = %v, want just the dropped folder", got)
	}
	if FindNode(newTree, pid("2023/August/Indore")) == nil {
		t.Error("dropping April's Indore should leave August's untouched")
	}
}

func TestDropNodesRejectsTopLevel(t *testing.T) {
	tree := groupedTree()

	newTree, _, err := DropNodes(tree, pids("2023"))
	if err == nil {
		t.Fatal("expected rejection dropping a top-level folder")
	}
	if len(newTree[0].Children) != 2 {
		t.Errorf("tree changed on a rejected drop: %d children, want 2", len(newTree[0].Children))
	}
}

func TestSortTreeOrdersSplicedChildren(t *testing.T) {
	tree := []Node{{ID: pid("2024"), Name: "2024", Children: []Node{
		{ID: pid("2024/June"), Name: "June"},
		{ID: pid("2024/April"), Name: "April"},
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
	tree[0].Children[0].Children[0].MergedIDs = append(tree[0].Children[0].Children[0].MergedIDs, 99)

	if clone[0].Children[0].Children[0].FileCount == 99 {
		t.Error("mutating the original mutated the clone's FileCount")
	}
	if len(clone[0].Children[0].Children[0].MergedIDs) != 0 {
		t.Error("mutating the original mutated the clone's MergedIDs")
	}
}

// fullBounds is a folder's whole range: its own Bounds AND every ancestor's.
func fullBounds(tree []Node, id int64) Bounds {
	b := Bounds{{}}
	for _, a := range chainTo(tree, id) {
		b = b.Intersect(FindNode(tree, a).Bounds)
	}
	return b
}

// matchesChain reports whether a file fits the folder and every ancestor.
func matchesChain(tree []Node, id int64, file Constraint) bool {
	for _, a := range chainTo(tree, id) {
		if !FindNode(tree, a).Bounds.Matches(file) {
			return false
		}
	}
	return true
}

// one is a folder's bounds with a single alternative.
func one(c Constraint) Bounds { return Bounds{c} }

// boundsTree is 2024/03_March/{01_03/{Mumbai,Panji,Baga}, 12/Panji,
// 20/{Canon,Manali}, 03/{Canon,Goa}}, every folder carrying what the planner
// would store for it.
func boundsTree() []Node {
	leaf := func(p, name string, c Constraint) Node {
		return Node{ID: pid(p), Name: name, FileCount: 1, Bounds: one(c)}
	}
	place := func(p, name string) Node { return leaf(p, name, Constraint{Location: []string{name}}) }
	canon := func(p string) Node { return leaf(p, "Canon", Constraint{Device: []string{"Canon"}}) }
	day := func(p, name string, days []int, children ...Node) Node {
		return Node{ID: pid(p), Name: name, Bounds: one(Constraint{Date: days}), Children: children}
	}
	return []Node{{ID: pid("b/2024"), Name: "2024", Bounds: one(Constraint{Year: []int{2024}}), Children: []Node{
		{ID: pid("b/2024/03"), Name: "03_March", Bounds: one(Constraint{Month: []int{3}}), Children: []Node{
			day("b/2024/03/01_03", "01_03", []int{1, 2, 3},
				place("b/2024/03/01_03/Mumbai", "Mumbai"),
				place("b/2024/03/01_03/Panji", "Panji"),
				place("b/2024/03/01_03/Baga", "Baga")),
			day("b/2024/03/12", "12", []int{12}, place("b/2024/03/12/Panji", "Panji")),
			day("b/2024/03/05", "05", []int{5}, canon("b/2024/03/05/Canon"), place("b/2024/03/05/Goa", "Goa")),
			day("b/2024/03/20", "20", []int{20}, canon("b/2024/03/20/Canon"), place("b/2024/03/20/Manali", "Manali")),
		}},
	}}}
}

// Each review edit transforms the folders' bounds (spec D14).
func TestEditsTransformBounds(t *testing.T) {
	march := Constraint{Year: []int{2024}, Month: []int{3}}
	with := func(c Constraint, f func(*Constraint)) Constraint { f(&c); return c }
	merge := func(ids ...string) func(t *testing.T, tree []Node) []Node {
		return func(t *testing.T, tree []Node) []Node {
			tree, _, _, _, err := MergeNodes(tree, pids(ids...))
			if err != nil {
				t.Fatal(err)
			}
			return tree
		}
	}
	tests := []struct {
		name string
		edit func(t *testing.T, tree []Node) []Node
		id   string // the folder checked afterwards
		own  Bounds
		full Bounds
		in   []Constraint // files that must fit the folder
		out  []Constraint // files that must not
	}{
		{
			name: "merge places then rename",
			edit: func(t *testing.T, tree []Node) []Node {
				tree = merge("b/2024/03/01_03/Mumbai", "b/2024/03/01_03/Panji", "b/2024/03/01_03/Baga")(t, tree)
				FindNode(tree, pid("b/2024/03/01_03/Mumbai")).Name = "Goa Trip" // a rename leaves bounds alone
				return tree
			},
			id:  "b/2024/03/01_03/Mumbai",
			own: one(Constraint{Location: []string{"Baga", "Mumbai", "Panji"}}),
			full: one(with(march, func(c *Constraint) {
				c.Date, c.Location = []int{1, 2, 3}, []string{"Baga", "Mumbai", "Panji"}
			})),
		},
		{
			name: "drop pushes into children",
			edit: func(t *testing.T, tree []Node) []Node {
				tree, _, err := DropNodes(tree, pids("b/2024/03/12"))
				if err != nil {
					t.Fatal(err)
				}
				return tree
			},
			id:   "b/2024/03/12/Panji",
			own:  one(Constraint{Date: []int{12}, Location: []string{"Panji"}}),
			full: one(with(march, func(c *Constraint) { c.Date, c.Location = []int{12}, []string{"Panji"} })),
		},
		{
			name: "flatten keeps its own",
			edit: func(t *testing.T, tree []Node) []Node {
				tree, _, _, err := FlattenNodes(tree, pids("b/2024/03"))
				if err != nil {
					t.Fatal(err)
				}
				return tree
			},
			id:   "b/2024/03",
			own:  one(Constraint{Month: []int{3}}),
			full: one(march),
		},
		{
			name: "merge unions same-named children",
			edit: merge("b/2024/03/01_03", "b/2024/03/12"),
			id:   "b/2024/03/01_03/Panji", // the 12/Panji folded into it
			own:  one(Constraint{Date: []int{1, 2, 3, 12}, Location: []string{"Panji"}}),
			full: one(with(march, func(c *Constraint) { c.Date, c.Location = []int{1, 2, 3, 12}, []string{"Panji"} })),
			out:  []Constraint{with(march, func(c *Constraint) { c.Date, c.Location = []int{8}, []string{"Panji"} })},
		},
		{
			name: "merge across days keeps the days",
			edit: merge("b/2024/03/05/Canon", "b/2024/03/20/Canon"),
			id:   "b/2024/03/05/Canon",
			own:  one(Constraint{Date: []int{5, 20}, Device: []string{"Canon"}}),
			in: []Constraint{
				with(march, func(c *Constraint) { c.Date, c.Device = []int{5}, []string{"Canon"} }),
				with(march, func(c *Constraint) { c.Date, c.Device = []int{20}, []string{"Canon"} }),
			},
			out: []Constraint{with(march, func(c *Constraint) { c.Date, c.Device = []int{10}, []string{"Canon"} })},
		},
		{
			name: "merge keeps each place to its own day",
			edit: merge("b/2024/03/05/Goa", "b/2024/03/20/Manali"),
			id:   "b/2024/03/05/Goa",
			own: Bounds{
				{Date: []int{5}, Location: []string{"Goa"}},
				{Date: []int{20}, Location: []string{"Manali"}},
			},
			in: []Constraint{
				with(march, func(c *Constraint) { c.Date, c.Location = []int{5}, []string{"Goa"} }),
				with(march, func(c *Constraint) { c.Date, c.Location = []int{20}, []string{"Manali"} }),
			},
			out: []Constraint{with(march, func(c *Constraint) { c.Date, c.Location = []int{20}, []string{"Goa"} })},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree := tt.edit(t, boundsTree())
			n := FindNode(tree, pid(tt.id))
			if n == nil {
				t.Fatalf("%s gone from the tree", tt.id)
			}
			if !reflect.DeepEqual(n.Bounds, tt.own) {
				t.Errorf("own bounds = %+v, want %+v", n.Bounds, tt.own)
			}
			if tt.full != nil {
				if got := fullBounds(tree, n.ID); !reflect.DeepEqual(got, tt.full) {
					t.Errorf("full bounds = %+v, want %+v", got, tt.full)
				}
			}
			for _, f := range tt.in {
				if !matchesChain(tree, n.ID, f) {
					t.Errorf("%+v does not fit, want it to", f)
				}
			}
			for _, f := range tt.out {
				if matchesChain(tree, n.ID, f) {
					t.Errorf("%+v fits, want it not to", f)
				}
			}
			if tt.name == "flatten keeps its own" && len(n.Children) != 0 {
				t.Errorf("children = %v, want none", n.Children)
			}
		})
	}
}

// Alternatives differing on one level fold into one; on two they stay apart.
// An intersection with nothing in common matches nothing, not everything.
func TestBoundsAlternatives(t *testing.T) {
	goa3 := one(Constraint{Date: []int{3}, Location: []string{"Goa"}})
	if got, want := goa3.Union(one(Constraint{Date: []int{20}, Location: []string{"Goa"}})),
		one(Constraint{Date: []int{3, 20}, Location: []string{"Goa"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("union = %+v, want %+v", got, want)
	}
	if got := goa3.Union(one(Constraint{Date: []int{20}, Location: []string{"Manali"}})); len(got) != 2 {
		t.Errorf("union = %+v, want two alternatives", got)
	}
	none := goa3.Intersect(one(Constraint{Location: []string{"Manali"}}))
	if len(none) != 0 || none.Matches(Constraint{Date: []int{3}, Location: []string{"Goa"}}) {
		t.Errorf("disjoint intersect = %+v, want no alternatives", none)
	}
	if v, err := Bounds(nil).Value(); err != nil || v != "[]" {
		t.Errorf("stored nothing as %v, want []", v)
	}
	if Bounds(nil).Matches(Constraint{}) || !(Bounds{{}}).Matches(Constraint{}) {
		t.Error("[] must match nothing and [{}] everything")
	}
}

// A folder can't be merged with one inside it — there is no common parent to
// lift both under.
func TestMergeNodesRejectsAncestor(t *testing.T) {
	tree := boundsTree()
	if _, _, _, _, err := MergeNodes(tree, pids("b/2024/03/05", "b/2024/03/05/Goa")); err == nil {
		t.Error("merged a folder with its own child, want an error")
	}
	if _, _, _, _, err := MergeNodes(tree, pids("b/2024/03/05/Goa", "b/2024/03/05")); err == nil {
		t.Error("merged a folder with its own parent, want an error")
	}
}
