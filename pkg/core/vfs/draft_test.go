// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
)

func TestDraftFile(t *testing.T) {
	dir := t.TempDir()
	if edits, err := ReadDraft(dir); err != nil || edits != nil {
		t.Fatalf("no file = %v, %v; want no edits", edits, err)
	}
	want := []Edit{
		{Seq: 1, Op: OpRename, Node: 17, From: "Panji", To: "Goa Trip"},
		{Seq: 2, Op: OpMerge, Nodes: []int64{21, 22, 23}},
		{Seq: 3, Op: OpDrop, Nodes: []int64{30}},
	}
	for _, e := range want {
		if err := AppendDraft(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := ReadDraft(dir); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("read back %+v, %v; want %+v", got, err, want)
	}

	// undo: the last line goes, the rest stays
	if err := WriteDraft(dir, want[:2]); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadDraft(dir); !reflect.DeepEqual(got, want[:2]) {
		t.Fatalf("after rewrite %+v, want %+v", got, want[:2])
	}

	// a crash mid-append leaves a torn last line: that edit never happened
	f, err := os.OpenFile(filepath.Join(dir, DraftFileName), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"seq":3,"op":"dr`)
	f.Close()
	if got, err := ReadDraft(dir); err != nil || !reflect.DeepEqual(got, want[:2]) {
		t.Fatalf("torn last line: %+v, %v; want %+v", got, err, want[:2])
	}
	// the next append after the crash must land on a line of its own, not
	// glue onto the fragment — twice, since the second is where it used to fail
	for _, e := range []Edit{{Seq: 3, Op: OpDrop, Nodes: []int64{30}}, {Seq: 4, Op: OpFlatten, Nodes: []int64{8}}} {
		if err := AppendDraft(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	after := append(append([]Edit{}, want[:2]...), Edit{Seq: 3, Op: OpDrop, Nodes: []int64{30}}, Edit{Seq: 4, Op: OpFlatten, Nodes: []int64{8}})
	if got, err := ReadDraft(dir); err != nil || !reflect.DeepEqual(got, after) {
		t.Fatalf("edits after a torn line: %+v, %v; want %+v", got, err, after)
	}

	// a crash between a whole edit and its newline: the edit counts, and the
	// next append must not glue onto it
	os.WriteFile(filepath.Join(dir, DraftFileName), []byte(`{"seq":1,"op":"drop","nodes":[30]}`), 0o644)
	if got, err := ReadDraft(dir); err != nil || len(got) != 1 {
		t.Fatalf("unterminated whole edit: %+v, %v; want it read", got, err)
	}
	if err := AppendDraft(dir, Edit{Seq: 2, Op: OpFlatten, Nodes: []int64{8}}); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadDraft(dir); err != nil || len(got) != 2 {
		t.Fatalf("after an unterminated edit: %+v, %v; want both edits", got, err)
	}

	// a bad line in the middle is not a crash artifact
	os.WriteFile(filepath.Join(dir, DraftFileName), []byte("nope\n{\"seq\":1}\n"), 0o644)
	if _, err := ReadDraft(dir); err == nil {
		t.Error("a bad line before the last must be an error")
	}

	if err := WriteDraft(dir, nil); err != nil {
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
	if err := AppendDraft(dir, Edit{Seq: 1, Op: OpRename, Node: leaf.ID, From: leaf.Name, To: "Manali"}); err != nil {
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
		Status     string `db:"status"`
	}
	read := func() []row {
		var rows []row
		if err := h.d.SQL.Select(&rows, `SELECT target_path, status FROM virtual_fs_entries ORDER BY id`); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	applied := read()
	for _, r := range applied {
		if r.Status != db.StatusApproved || !strings.Contains("/"+r.TargetPath, "/Manali/") {
			t.Errorf("after apply %+v, want APPROVED under Manali", r)
		}
	}

	// crash between the commit and the file removal: the same draft is back
	if err := AppendDraft(dir, Edit{Seq: 1, Op: OpRename, Node: leaf.ID, From: leaf.Name, To: "Manali"}); err != nil {
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
	merged := Replay(CloneTree(tree), []Edit{{Op: OpMerge, Nodes: []int64{3, 4}}})
	if FindNode(merged, 4) != nil || FindNode(merged, 3) == nil {
		t.Fatalf("merge did not fold 04 into 03: %+v", merged)
	}
	// once applied, 04 is gone: the same edits replay to the same tree
	again := Replay(CloneTree(merged), []Edit{
		{Op: OpMerge, Nodes: []int64{3, 4}},
		{Op: OpDrop, Nodes: []int64{99}},
		{Op: OpRename, Node: 99, To: "x"},
	})
	if !reflect.DeepEqual(again, merged) {
		t.Errorf("replay over applied tree changed it:\n got %+v\nwant %+v", again, merged)
	}
}
