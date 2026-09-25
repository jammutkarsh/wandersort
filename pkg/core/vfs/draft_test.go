// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
)

func TestDraftFile(t *testing.T) {
	dir := t.TempDir()
	if edits, err := readDraft(dir); err != nil || edits != nil {
		t.Fatalf("no file = %v, %v; want no edits", edits, err)
	}
	want := []Edit{
		{Seq: 1, Op: OpRename, Node: 17, From: "Panji", To: "Goa Trip"},
		{Seq: 2, Op: OpMerge, Nodes: []int64{21, 22, 23}},
		{Seq: 3, Op: OpDrop, Nodes: []int64{30}},
	}
	for _, e := range want {
		if err := appendDraft(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := readDraft(dir); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("read back %+v, %v; want %+v", got, err, want)
	}

	// undo: the last line goes, the rest stays
	if err := writeDraft(dir, want[:2]); err != nil {
		t.Fatal(err)
	}
	if got, _ := readDraft(dir); !reflect.DeepEqual(got, want[:2]) {
		t.Fatalf("after rewrite %+v, want %+v", got, want[:2])
	}

	// a crash mid-append leaves a torn last line: that edit never happened
	f, err := os.OpenFile(filepath.Join(dir, DraftFileName), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"seq":3,"op":"dr`)
	f.Close()
	if got, err := readDraft(dir); err != nil || !reflect.DeepEqual(got, want[:2]) {
		t.Fatalf("torn last line: %+v, %v; want %+v", got, err, want[:2])
	}
	// the next append after the crash must land on a line of its own, not
	// glue onto the fragment — twice, since the second is where it used to fail
	for _, e := range []Edit{{Seq: 3, Op: OpDrop, Nodes: []int64{30}}, {Seq: 4, Op: OpFlatten, Nodes: []int64{8}}} {
		if err := appendDraft(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	after := append(append([]Edit{}, want[:2]...), Edit{Seq: 3, Op: OpDrop, Nodes: []int64{30}}, Edit{Seq: 4, Op: OpFlatten, Nodes: []int64{8}})
	if got, err := readDraft(dir); err != nil || !reflect.DeepEqual(got, after) {
		t.Fatalf("edits after a torn line: %+v, %v; want %+v", got, err, after)
	}

	// a crash between a whole edit and its newline: the edit counts, and the
	// next append must not glue onto it
	os.WriteFile(filepath.Join(dir, DraftFileName), []byte(`{"seq":1,"op":"drop","nodes":[30]}`), 0o644)
	if got, err := readDraft(dir); err != nil || len(got) != 1 {
		t.Fatalf("unterminated whole edit: %+v, %v; want it read", got, err)
	}
	if err := appendDraft(dir, Edit{Seq: 2, Op: OpFlatten, Nodes: []int64{8}}); err != nil {
		t.Fatal(err)
	}
	if got, err := readDraft(dir); err != nil || len(got) != 2 {
		t.Fatalf("after an unterminated edit: %+v, %v; want both edits", got, err)
	}

	// a bad line in the middle is not a crash artifact
	os.WriteFile(filepath.Join(dir, DraftFileName), []byte("nope\n{\"seq\":1}\n"), 0o644)
	if _, err := readDraft(dir); err == nil {
		t.Error("a bad line before the last must be an error")
	}

	if err := writeDraft(dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, DraftFileName)); !os.IsNotExist(err) {
		t.Errorf("no edits must remove the file, got %v", err)
	}
}

// TestApplyDraft covers spec D17/D18 end to end: the draft reaches the
// database only through ApplyDraft, which approves the plan and removes the
// file; and a crash after the commit but before the removal replays onto the
// applied tree as a no-op.
func TestApplyDraft(t *testing.T) {
	h := newHarness(t)
	h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
	h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:03 16:00:00", 0, 0, 3024, 4032))
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleLocation}
	h.build(t, cfg, installtest.Resolver(t))
	ctx := context.Background()
	dir := t.TempDir()

	tree, err := BuildTree(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	leaf := leafNodes(tree)[0]
	if err := appendDraft(dir, Edit{Seq: 1, Op: OpRename, Node: leaf.ID, From: leaf.Name, To: "Manali"}); err != nil {
		t.Fatal(err)
	}

	// the draft is not in the database until it is applied
	var targets []string
	if err := h.d.SQL.Select(&targets, `SELECT target_path FROM virtual_fs_entries`); err != nil {
		t.Fatal(err)
	}
	for _, p := range targets {
		if strings.Contains(p, "Manali") {
			t.Fatalf("draft edit reached the database before apply: %q", p)
		}
	}

	if err := ApplyDraft(ctx, h.d, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, DraftFileName)); !os.IsNotExist(err) {
		t.Errorf("draft still there after apply: %v", err)
	}
	type row struct {
		TargetPath string `db:"target_path"`
	}
	read := func() []row {
		var rows []row
		if err := h.d.SQL.Select(&rows, `SELECT target_path FROM virtual_fs_entries ORDER BY id`); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	applied := read()
	for _, r := range applied {
		if !strings.Contains("/"+r.TargetPath, "/Manali/") {
			t.Errorf("after apply %+v, want under Manali", r)
		}
	}

	// crash between the commit and the file removal: the same draft is back
	if err := appendDraft(dir, Edit{Seq: 1, Op: OpRename, Node: leaf.ID, From: leaf.Name, To: "Manali"}); err != nil {
		t.Fatal(err)
	}
	if err := ApplyDraft(ctx, h.d, dir); err != nil {
		t.Fatal(err)
	}
	if again := read(); !reflect.DeepEqual(again, applied) {
		t.Errorf("replaying an applied draft changed the plan:\n got %+v\nwant %+v", again, applied)
	}
}

// TestReplaySkipsMissingNodes pins idempotency for the reshaping ops: a merge
// or drop naming folders that are gone does nothing.
func TestReplaySkipsMissingNodes(t *testing.T) {
	tree := []Node{{ID: 1, Name: "2024", Level: LevelYear, Children: []Node{
		{ID: 2, Name: "06_June", Level: LevelMonth, Children: []Node{
			{ID: 3, Name: "03", FileCount: 1},
			{ID: 4, Name: "04", FileCount: 1},
		}},
	}}}
	merged := replay(cloneTree(tree), []Edit{{Op: OpMerge, Nodes: []int64{3, 4}}})
	if FindNode(merged, 4) != nil || FindNode(merged, 3) == nil {
		t.Fatalf("merge did not fold 04 into 03: %+v", merged)
	}
	// once applied, 04 is gone: the same edits replay to the same tree
	again := replay(cloneTree(merged), []Edit{
		{Op: OpMerge, Nodes: []int64{3, 4}},
		{Op: OpDrop, Nodes: []int64{99}},
		{Op: OpRename, Node: 99, To: "x"},
	})
	if !reflect.DeepEqual(again, merged) {
		t.Errorf("replay over applied tree changed it:\n got %+v\nwant %+v", again, merged)
	}
}

// A draft written before the move to json/v2 is read the same, and a merge,
// which names no single node, still doesn't write a "node" key.
func TestDraftReadsLinesWrittenByEncodingJSONv1(t *testing.T) {
	dir := t.TempDir()
	v1 := `{"seq":1,"op":"rename","node":17,"from":"Panji","to":"Goa Trip"}` + "\n" +
		`{"seq":2,"op":"merge","nodes":[4,5]}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, DraftFileName), []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	edits, err := readDraft(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Edit{
		{Seq: 1, Op: OpRename, Node: 17, From: "Panji", To: "Goa Trip"},
		{Seq: 2, Op: OpMerge, Nodes: []int64{4, 5}},
	}
	if !reflect.DeepEqual(edits, want) {
		t.Fatalf("edits = %+v, want %+v", edits, want)
	}
	if err := writeDraft(dir, edits); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, DraftFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != v1 {
		t.Errorf("rewritten draft =\n%s\nwant it byte-identical to\n%s", got, v1)
	}
}

// draftTree is a year, a month and two days — enough for every edit op.
func draftTree() []Node {
	return []Node{{ID: 1, Name: "2024", Level: LevelYear, FileCount: 2, Children: []Node{
		{ID: 2, Name: "06_June", Level: LevelMonth, FileCount: 2, Children: []Node{
			{ID: 3, Name: "03", FileCount: 1, Children: []Node{{ID: 5, Name: "Goa", FileCount: 1}}},
			{ID: 4, Name: "04", FileCount: 1},
		}},
	}}}
}

// TestDraftSession covers the Draft interface the review drives: an edit
// lands in the tree and the journal together, a reopen replays it to the same
// tree, a refusal changes neither, and Undo/Reset go back without touching
// the plan the draft was opened on.
func TestDraftSession(t *testing.T) {
	open := func(t *testing.T, dir string, base []Node) *Draft {
		t.Helper()
		d, err := OpenDraft(dir, base)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	t.Run("apply journals and a reopen replays to the same tree", func(t *testing.T) {
		dir := t.TempDir()
		d := open(t, dir, draftTree())
		out, err := d.Apply(Edit{Op: OpRename, Node: 3, From: "03", To: "Goa Trip"})
		if err != nil || out.Focus != 3 || out.Name != "Goa Trip" {
			t.Fatalf("rename = %+v, %v", out, err)
		}
		out, err = d.Apply(Edit{Op: OpMerge, Nodes: []int64{4, 3}})
		if err != nil || out.Focus != 4 || out.Parent != "06_June" {
			t.Fatalf("merge = %+v, %v", out, err)
		}
		if got := d.Edits(); len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 {
			t.Fatalf("edits = %+v, want two, numbered 1 and 2", got)
		}
		again := open(t, dir, draftTree())
		if !reflect.DeepEqual(again.Tree(), d.Tree()) {
			t.Errorf("reopened tree differs from the live one:\n got %+v\nwant %+v", again.Tree(), d.Tree())
		}
	})

	t.Run("a refused edit changes nothing", func(t *testing.T) {
		dir := t.TempDir()
		d := open(t, dir, draftTree())
		before := cloneTree(d.Tree())
		for _, e := range []Edit{
			{Op: OpRename, Node: 1, To: "2025"},   // a year is fixed
			{Op: OpRename, Node: 3, To: ""},       // no name
			{Op: OpRename, Node: 99, To: "x"},     // no such folder
			{Op: OpMerge, Nodes: []int64{3}},      // one folder
			{Op: OpDrop, Nodes: []int64{2}},       // a month is fixed
			{Op: OpFlatten, Nodes: []int64{4}},    // nothing below it
			{Op: "shuffle", Nodes: []int64{3, 4}}, // not an edit
		} {
			if _, err := d.Apply(e); err == nil {
				t.Errorf("%+v: want refused", e)
			}
		}
		if _, err := d.Apply(Edit{Op: OpRename, Node: 2, To: "x"}); !errors.Is(err, ErrFixedFolder) {
			t.Errorf("renaming a month: err = %v, want ErrFixedFolder", err)
		}
		if !reflect.DeepEqual(d.Tree(), before) || len(d.Edits()) != 0 || len(open(t, dir, draftTree()).Edits()) != 0 {
			t.Error("a refused edit changed the tree or reached the journal")
		}
	})

	t.Run("a journal that can't be written undoes the edit", func(t *testing.T) {
		d := open(t, filepath.Join(t.TempDir(), "gone"), draftTree())
		if _, err := d.Apply(Edit{Op: OpRename, Node: 3, To: "Goa Trip"}); err == nil {
			t.Fatal("want the journal error")
		}
		if n := FindNode(d.Tree(), 3); n.Name != "03" || len(d.Edits()) != 0 {
			t.Errorf("tree shows %q with %d edits; want the edit undone", n.Name, len(d.Edits()))
		}
	})

	t.Run("undo and reset go back to the plan", func(t *testing.T) {
		dir := t.TempDir()
		base := draftTree()
		d := open(t, dir, base)
		if _, err := d.Undo(); !errors.Is(err, ErrNothingToUndo) {
			t.Errorf("undo with no edits: %v, want ErrNothingToUndo", err)
		}
		d.Apply(Edit{Op: OpRename, Node: 3, To: "One"})
		d.Apply(Edit{Op: OpFlatten, Nodes: []int64{3}})
		last, err := d.Undo()
		if err != nil || last.Op != OpFlatten {
			t.Fatalf("undo = %+v, %v; want the flatten back", last, err)
		}
		if n := FindNode(d.Tree(), 3); n.Name != "One" || len(n.Children) != 1 {
			t.Errorf("after undo %+v, want the rename kept and Goa back", n)
		}
		if got := open(t, dir, draftTree()).Edits(); len(got) != 1 {
			t.Errorf("journal after undo = %+v, want one edit", got)
		}
		if err := d.Reset(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(d.Tree(), draftTree()) || len(d.Edits()) != 0 {
			t.Errorf("after reset %+v, want the plan as proposed", d.Tree())
		}
		if _, err := os.Stat(filepath.Join(dir, DraftFileName)); !os.IsNotExist(err) {
			t.Errorf("journal still there after reset: %v", err)
		}
		if !reflect.DeepEqual(base, draftTree()) {
			t.Error("the plan the draft was opened on was edited in place")
		}
	})
}
