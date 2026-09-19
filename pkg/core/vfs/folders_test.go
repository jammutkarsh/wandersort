// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
)

// folderIDs maps every folder path in the review tree to its ID.
func folderIDs(t *testing.T, d *db.DB) map[string]int64 {
	t.Helper()
	tree, err := BuildTree(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	var walk func(nodes []Node, parent string)
	walk = func(nodes []Node, parent string) {
		for _, n := range nodes {
			p := parent + n.Name
			out[p] = n.ID
			walk(n.Children, p+"/")
		}
	}
	walk(tree, "")
	return out
}

func folderCount(t *testing.T, d *db.DB) int {
	t.Helper()
	var n int
	if err := d.SQL.Get(&n, `SELECT COUNT(*) FROM folder_nodes`); err != nil {
		t.Fatal(err)
	}
	return n
}

// eventTree is two unlocated clusters days apart: two sibling event folders
// under one month, the folders a reviewer renames and merges.
func eventTree(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
	h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:20 14:00:00", 0, 0, 3024, 4032))
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleLocation}
	h.build(t, cfg, nil)
	return h
}

// Planning again with nothing changed must give every folder back its id, or
// an edit naming a folder could not survive a re-plan.
func TestFolderIDsSurviveReplan(t *testing.T) {
	h := eventTree(t)
	before := folderIDs(t, h.d)
	if len(before) == 0 {
		t.Fatal("empty tree")
	}
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleLocation}
	h.build(t, cfg, nil)

	after := folderIDs(t, h.d)
	if len(after) != len(before) {
		t.Fatalf("folders = %v, want the same %d as before", after, len(before))
	}
	for p, id := range before {
		if after[p] != id {
			t.Errorf("%s: id %d, want %d", p, after[p], id)
		}
	}
	if n := folderCount(t, h.d); n != len(before) {
		t.Errorf("%d folder rows, want %d (nothing left behind)", n, len(before))
	}
}

// Every folder is stored with the level that made it.
func TestFolderLevels(t *testing.T) {
	h := newHarness(t)
	h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 3024, 4032))
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleOrientation}
	cfg.CollapseLevels = false
	h.build(t, cfg, nil)

	var levels []string
	if err := h.d.SQL.Select(&levels, `SELECT level FROM folder_nodes ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	want := []string{LevelYear, LevelMonth, RuleDate, RuleOrientation}
	if len(levels) != len(want) {
		t.Fatalf("levels = %v, want %v", levels, want)
	}
	for i := range want {
		if levels[i] != want[i] {
			t.Errorf("levels = %v, want %v", levels, want)
			break
		}
	}
}

// A rename changes the folder's name, not its id.
func TestConfirmRenameKeepsFolderID(t *testing.T) {
	h := eventTree(t)
	ctx := context.Background()
	tree, err := BuildTree(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	leaf := leafNodes(tree)[0]
	id := leaf.ID
	leaf.Name = "Manali"
	if err := Confirm(ctx, h.d, tree); err != nil {
		t.Fatal(err)
	}

	var name string
	if err := h.d.SQL.Get(&name, `SELECT name FROM folder_nodes WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if name != "Manali" {
		t.Errorf("folder %d is named %q, want Manali", id, name)
	}
}

// A merge moves the folded folder's files onto the survivor and deletes the
// folded folder's row, since nothing uses it any more.
func TestConfirmMergeDeletesFoldedFolder(t *testing.T) {
	h := eventTree(t)
	ctx := context.Background()
	tree, err := BuildTree(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	before := folderCount(t, h.d)
	leaves := leafNodes(tree)
	keep, folded := leaves[0].ID, leaves[1].ID
	leaves[0].MergedIDs = []int64{folded}
	tree = dropNodeByID(tree, folded)
	if err := Confirm(ctx, h.d, tree); err != nil {
		t.Fatal(err)
	}

	var gone int
	if err := h.d.SQL.Get(&gone, `SELECT COUNT(*) FROM folder_nodes WHERE id = ?`, folded); err != nil {
		t.Fatal(err)
	}
	if gone != 0 {
		t.Errorf("folded folder %d still stored", folded)
	}
	if n := folderCount(t, h.d); n != before-1 {
		t.Errorf("%d folder rows, want %d", n, before-1)
	}
	var nodes []int64
	if err := h.d.SQL.Select(&nodes, `SELECT DISTINCT node_id FROM virtual_fs_entries`); err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0] != keep {
		t.Errorf("files sit in folders %v, want only %d", nodes, keep)
	}
}

// Two sibling folders renamed to one name become one folder, so the tree
// read back afterwards shows one, not two same-named rows.
func TestConfirmSameNameSiblingsBecomeOneFolder(t *testing.T) {
	h := eventTree(t)
	ctx := context.Background()
	tree, err := BuildTree(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	leaves := leafNodes(tree)
	leaves[0].Name, leaves[1].Name = "Manali", "Manali"
	if err := Confirm(ctx, h.d, tree); err != nil {
		t.Fatal(err)
	}

	tree, err = BuildTree(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	leaves = leafNodes(tree)
	if len(leaves) != 1 || leaves[0].Name != "Manali" || leaves[0].FileCount != 2 {
		t.Errorf("leaves = %+v, want one Manali holding both files", leaves)
	}
}

// A new proposal never reuses a folder holding a placed file, so a review
// rename of the proposal can't rename where the placed file is recorded.
func TestPlacedFolderIsNotReused(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
	placed := h.addFile(t, "lib/B.HEIC", "IMAGE", metaWith("2024:06:20 14:00:00", 0, 0, 3024, 4032))
	if _, err := h.d.ExecContext(ctx, `UPDATE file_registry SET placed = 1 WHERE id = ?`, placed); err != nil {
		t.Fatal(err)
	}
	placedFolder := dbtest.SeedEntry(t, h.d, placed, "2024/06_June/B.HEIC", "2024/06_June/B.HEIC", db.StatusDone)

	cfg := DefaultConfig()
	cfg.Rules = nil // plain Year/Month: the proposal wants the placed file's folder
	h.build(t, cfg, nil)

	ids := folderIDs(t, h.d)
	if got := ids["2024/06_June"]; got == 0 || got == placedFolder {
		t.Errorf("proposal folder = %d, want a new folder, not the placed one (%d)", got, placedFolder)
	}
}

// Merging device folders from two different days moves the files out from
// under their old day's place folder, which the save then deletes. The files
// lose that GPS link instead of the save failing on it (a review finding:
// "delete unused folders: FOREIGN KEY constraint failed").
func TestConfirmMergeAcrossDaysDropsOldPlaceFolder(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5439, 73.7553, 3024, 4032))
	h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:20 10:00:00", 15.5439, 73.7553, 3024, 4032))
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation, RuleDevice}
	cfg.CollapseLevels = false
	h.build(t, cfg, installtest.Resolver(t))

	tree, err := BuildTree(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	leaves := leafNodes(tree)
	if len(leaves) != 2 {
		t.Fatalf("want two device folders, got %d", len(leaves))
	}
	tree, _, _, _, err = MergeNodes(tree, []int64{leaves[0].ID, leaves[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := Confirm(ctx, h.d, tree); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	var nodes []int64
	if err := h.d.SQL.Select(&nodes, `SELECT DISTINCT node_id FROM virtual_fs_entries`); err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Errorf("files sit in folders %v, want one merged folder", nodes)
	}
}

// A copy stopped partway leaves a copied and a still-approved file in one
// folder. Renaming that folder in review must not rename where the copied
// file is recorded: on disk it stays where it was copied to.
func TestConfirmRenameLeavesCopiedFilesFolder(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	copied := h.addFile(t, "lib/A.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 3024, 4032))
	pending := h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:03 11:00:00", 0, 0, 3024, 4032))
	if _, err := h.d.ExecContext(ctx, `UPDATE file_registry SET placed = 1 WHERE id = ?`, copied); err != nil {
		t.Fatal(err)
	}
	folder := dbtest.SeedEntry(t, h.d, copied, "2024/06_June/A.HEIC", "2024/06_June/A.HEIC", db.StatusDone)
	dbtest.SeedEntry(t, h.d, pending, "/src/dump/B.HEIC", "2024/06_June/B.HEIC", db.StatusApproved)
	// a July folder under the same year, with nothing copied in it
	other := h.addFile(t, "dump/C.HEIC", "IMAGE", metaWith("2024:07:03 11:00:00", 0, 0, 3024, 4032))
	dbtest.SeedEntry(t, h.d, other, "/src/dump/C.HEIC", "2024/07_July/C.HEIC", db.StatusApproved)

	tree, err := BuildTree(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	tree[0].Name = "2024 Trip"
	if err := Confirm(ctx, h.d, tree); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	folders, err := loadFolderRows(ctx, h.d.SQL)
	if err != nil {
		t.Fatal(err)
	}
	if got := folderPath(folders, map[int64]string{}, folder); got != "2024/06_June" {
		t.Errorf("copied file's folder is %q, want 2024/06_June as on disk", got)
	}
	for id, want := range map[int64]string{
		pending: "2024-Trip/06_June/B.HEIC",
		other:   "2024-Trip/07_July/C.HEIC",
	} {
		var got string
		if err := h.d.SQL.Get(&got, `SELECT target_path FROM virtual_fs_entries WHERE file_id = ?`, id); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("file %d: target_path = %q, want %q", id, got, want)
		}
	}
}

// A copy stopped partway, then a scan: the files still waiting to be copied
// and the new scan's files for the same month share one folder in review,
// not two same-named siblings.
func TestReplanJoinsLeftoversOfStoppedCopy(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	copied := h.addFile(t, "lib/A.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 3024, 4032))
	pending := h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:03 11:00:00", 0, 0, 3024, 4032))
	if _, err := h.d.ExecContext(ctx, `UPDATE file_registry SET placed = 1 WHERE id = ?`, copied); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedEntry(t, h.d, copied, "2024/06_June/A.HEIC", "2024/06_June/A.HEIC", db.StatusDone)
	dbtest.SeedEntry(t, h.d, pending, "/src/dump/B.HEIC", "2024/06_June/B.HEIC", db.StatusApproved)
	h.addFile(t, "dump/C.HEIC", "IMAGE", metaWith("2024:06:05 11:00:00", 0, 0, 3024, 4032))

	cfg := DefaultConfig()
	cfg.Rules = nil
	h.build(t, cfg, nil)

	tree, err := BuildTree(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 1 || len(tree[0].Children) != 1 {
		t.Fatalf("want one 2024 holding one month, got %+v", tree)
	}
	if month := tree[0].Children[0]; month.Name != "06_June" || month.FileCount != 2 {
		t.Errorf("month = %s with %d files, want 06_June with the leftover and the new file",
			month.Name, month.FileCount)
	}
}

// A name typed in review is stored NFC, like every name the planner writes.
func TestConfirmStoresTypedNamesNFC(t *testing.T) {
	h := eventTree(t)
	ctx := context.Background()
	tree, err := BuildTree(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	leaf := leafNodes(tree)[0]
	leaf.Name = "Cafe\u0301" // NFD
	if err := Confirm(ctx, h.d, tree); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := h.d.SQL.Get(&name, `SELECT name FROM folder_nodes WHERE id = ?`, leaf.ID); err != nil {
		t.Fatal(err)
	}
	if name != "Caf\u00e9" {
		t.Errorf("name = %+q, want NFC %+q", name, "Caf\u00e9")
	}
}
