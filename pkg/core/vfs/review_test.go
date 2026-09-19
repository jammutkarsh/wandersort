// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
)

// leafNodes collects every childless node — with rules [location] and no GPS
// these are the dated event folders a reviewer renames.
func leafNodes(nodes []Node) []*Node {
	var out []*Node
	for i := range nodes {
		if len(nodes[i].Children) == 0 {
			out = append(out, &nodes[i])
			continue
		}
		out = append(out, leafNodes(nodes[i].Children)...)
	}
	return out
}

// renameFirstLeaf renames the first leaf folder and returns its old name so
// the test can assert on the rewrite.
func renameFirstLeaf(nodes []Node, newName string) (oldName string, ok bool) {
	leaves := leafNodes(nodes)
	if len(leaves) == 0 {
		return "", false
	}
	oldName = leaves[0].Name
	leaves[0].Name = newName
	return oldName, true
}

func TestReview(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"ReviewBuildAndConfirm", func(t *testing.T) {
			h := newHarness(t)
			// two unlocated files, same folder + day → one cluster, one dated
			// event folder (the node a reviewer renames)
			h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
			h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:03 16:00:00", 0, 0, 3024, 4032))
			cfg := DefaultConfig()
			cfg.Rules = []string{RuleLocation} // eventSegment needs no date level ahead of it
			h.build(t, cfg, installtest.Resolver(t))

			ctx := context.Background()
			tree, err := BuildTree(ctx, h.d)
			if err != nil {
				t.Fatal(err)
			}
			if len(tree) == 0 {
				t.Fatal("empty tree")
			}

			oldName, ok := renameFirstLeaf(tree, "Manali")
			if !ok {
				t.Fatal("no folder to rename")
			}

			if err := Confirm(ctx, h.d, tree); err != nil {
				t.Fatal(err)
			}

			var rows []struct {
				TargetPath string `db:"target_path"`
				Status     string `db:"status"`
			}
			if err := h.d.SQL.Select(&rows,
				`SELECT target_path, status FROM virtual_fs_entries`); err != nil {
				t.Fatal(err)
			}
			oldSeg := "/" + oldName + "/"
			for _, r := range rows {
				if r.Status != db.StatusApproved {
					t.Errorf("status = %q, want APPROVED", r.Status)
				}
				if !strings.Contains("/"+r.TargetPath, "/Manali/") {
					t.Errorf("target %q not rewritten to Manali", r.TargetPath)
				}
				if strings.Contains("/"+r.TargetPath, oldSeg) {
					t.Errorf("target %q still carries old segment %q", r.TargetPath, oldSeg)
				}
			}

			// the name the reviewer typed is remembered, so the next review's
			// rename completions offer it
			var labels []struct {
				Label string `db:"label"`
				Kind  string `db:"kind"`
			}
			if err := h.d.SQL.Select(&labels,
				`SELECT label, kind FROM user_labels`); err != nil {
				t.Fatal(err)
			}
			if len(labels) != 1 || labels[0].Label != "Manali" || labels[0].Kind != "EVENT" {
				t.Fatalf("labels = %+v, want one EVENT Manali", labels)
			}
		}},
		{"ReviewConfirmRejectsUnknownID", func(t *testing.T) {
			h := newHarness(t)
			h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
			h.build(t, DefaultConfig(), installtest.Resolver(t))

			bogus := []Node{{ID: 999999, Name: "x", Children: []Node{}}}
			if err := Confirm(context.Background(), h.d, bogus); err == nil {
				t.Fatal("expected error for unknown node id")
			}
		}},
		// TestReviewConfirmMergesCollidingRenames covers the real case a user hit:
		// two separate unresolved date clusters turn out to be the same place.
		// Renaming both to the same name is a deliberate merge, not an error — both
		// dirs collapse onto one final path and get one deduped EVENT label.
		{"ReviewConfirmMergesCollidingRenames", func(t *testing.T) {
			h := newHarness(t)
			// two unlocated clusters days apart → two sibling event dirs under June
			h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
			h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:20 14:00:00", 0, 0, 3024, 4032))
			cfg := DefaultConfig()
			cfg.Rules = []string{RuleLocation} // eventSegment needs no date level ahead of it
			h.build(t, cfg, installtest.Resolver(t))

			ctx := context.Background()
			tree, err := BuildTree(ctx, h.d)
			if err != nil {
				t.Fatal(err)
			}
			suggested := leafNodes(tree)
			if len(suggested) < 2 {
				t.Fatalf("want two sibling event folders, got %d", len(suggested))
			}
			suggested[0].Name = "Manali"
			suggested[1].Name = "Manali"

			if err := Confirm(ctx, h.d, tree); err != nil {
				t.Fatalf("Confirm merge: %v", err)
			}

			var targets []string
			if err := h.d.SQL.Select(&targets,
				`SELECT DISTINCT target_path FROM virtual_fs_entries`); err != nil {
				t.Fatal(err)
			}
			for _, tp := range targets {
				if !strings.Contains(tp, "/Manali/") {
					t.Errorf("target_path = %q, want merged under Manali", tp)
				}
			}

			var labels []struct {
				Label string `db:"label"`
			}
			if err := h.d.SQL.Select(&labels, `SELECT label FROM user_labels WHERE kind = 'EVENT'`); err != nil {
				t.Fatal(err)
			}
			if len(labels) != 1 || labels[0].Label != "Manali" {
				t.Fatalf("labels = %+v, want exactly one deduped Manali EVENT label", labels)
			}
		}},
		// TestReviewConfirmRemapsMergedIDs covers the review TUI's merge: the folded-
		// away node is gone from the submitted tree, so MergedIDs on the survivor is
		// the only thing telling Confirm its files still need remapping — without it
		// they'd silently keep their old target_path.
		{"ReviewConfirmRemapsMergedIDs", func(t *testing.T) {
			h := newHarness(t)
			h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
			h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:20 14:00:00", 0, 0, 3024, 4032))
			cfg := DefaultConfig()
			cfg.Rules = []string{RuleLocation} // eventSegment needs no date level ahead of it
			h.build(t, cfg, installtest.Resolver(t))

			ctx := context.Background()
			tree, err := BuildTree(ctx, h.d)
			if err != nil {
				t.Fatal(err)
			}
			suggested := leafNodes(tree)
			if len(suggested) < 2 {
				t.Fatalf("want two sibling event folders, got %d", len(suggested))
			}
			foldedID := suggested[1].ID
			suggested[0].Name = "Manali"
			suggested[0].MergedIDs = []int64{foldedID}
			tree = dropNodeByID(tree, foldedID)

			if err := Confirm(ctx, h.d, tree); err != nil {
				t.Fatalf("Confirm merge: %v", err)
			}

			var targets []string
			if err := h.d.SQL.Select(&targets,
				`SELECT target_path FROM virtual_fs_entries`); err != nil {
				t.Fatal(err)
			}
			if len(targets) != 2 {
				t.Fatalf("got %d entries, want 2", len(targets))
			}
			for _, tp := range targets {
				if !strings.Contains(tp, "/Manali/") {
					t.Errorf("target_path = %q, want merged under Manali", tp)
				}
			}
		}},
		// TestConfirmSuffixesCollidingBasenames covers a data-loss risk opened when
		// Confirm stopped rejecting colliding renames: collapsing two dirs onto one
		// can land two *different* masters on the same basename (phone counters get
		// reused across shoots). buildTargets' uniqueness only held for the layout it
		// generated, so Confirm has to re-establish it or the Execute phase would copy
		// one file over the other.
		{"ConfirmSuffixesCollidingBasenames", func(t *testing.T) {
			h := newHarness(t)
			// two unlocated clusters days apart → two sibling event dirs, same filename
			h.addFile(t, "dumpA/IMG_0042.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
			h.addFile(t, "dumpB/IMG_0042.HEIC", "IMAGE", metaWith("2024:06:20 14:00:00", 0, 0, 3024, 4032))
			cfg := DefaultConfig()
			cfg.Rules = []string{RuleLocation} // eventSegment needs no date level ahead of it
			h.build(t, cfg, nil)

			ctx := context.Background()
			tree, err := BuildTree(ctx, h.d)
			if err != nil {
				t.Fatal(err)
			}
			suggested := leafNodes(tree)
			if len(suggested) != 2 {
				t.Fatalf("want 2 renameable dirs, got %d", len(suggested))
			}
			// merge them onto one folder — both files now want the same basename
			suggested[0].Name, suggested[1].Name = "Manali", "Manali"

			if err := Confirm(ctx, h.d, tree); err != nil {
				t.Fatalf("Confirm: %v", err)
			}

			var targets []string
			if err := h.d.SQL.Select(&targets,
				`SELECT target_path FROM virtual_fs_entries ORDER BY target_path`); err != nil {
				t.Fatal(err)
			}
			if len(targets) != 2 {
				t.Fatalf("targets = %v, want 2", targets)
			}
			if targets[0] == targets[1] {
				t.Fatalf("both files landed on %q — one would overwrite the other", targets[0])
			}
			for _, tp := range targets {
				if !strings.Contains(tp, "/Manali/") {
					t.Errorf("target_path = %q, want merged under Manali", tp)
				}
			}
		}},
		// TestFilesUnderHandlesGlobMetacharacters covers folder names containing GLOB
		// wildcards. sanitizeSegment only rewrites path separators, so a reviewer can
		// legitimately name a folder "Goa [2024]" — read as a pattern its brackets are
		// a character class that matches nothing, and peek reported an empty folder
		// that plainly had files.
		{"FilesUnderHandlesGlobMetacharacters", func(t *testing.T) {
			h := newHarness(t)
			h.addFile(t, "dump/DSC_0001.JPG", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 4000, 3000))
			cfg := DefaultConfig()
			cfg.Rules = []string{RuleLocation} // eventSegment needs no date level ahead of it
			h.build(t, cfg, nil)

			ctx := context.Background()
			tree, err := BuildTree(ctx, h.d)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := renameFirstLeaf(tree, "Goa [2024]"); !ok {
				t.Fatal("no renameable node in the proposal")
			}
			if err := Confirm(ctx, h.d, tree); err != nil {
				t.Fatal(err)
			}

			tree, err = BuildTree(ctx, h.d)
			if err != nil {
				t.Fatal(err)
			}
			var bracketed int64
			var walk func([]Node)
			walk = func(ns []Node) {
				for i := range ns {
					if strings.Contains(ns[i].Name, "[2024]") {
						bracketed = ns[i].ID
					}
					walk(ns[i].Children)
				}
			}
			walk(tree)
			if bracketed == 0 {
				t.Fatal("rename to a bracketed name did not reach target_path")
			}

			files, err := FilesUnder(ctx, bracketed, h.d)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 1 {
				t.Errorf("FilesUnder(%d) = %v, want the one file under it", bracketed, files)
			}
		}},
		{"BuildTreeExcludesOrphan", func(t *testing.T) {
			h := newHarness(t)
			h.addFile(t, "d/IMG_0042.AAE", "SIDECAR", classifier.CommonMetadata{}) // no pair — routed to OrphanDir
			h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
			h.build(t, DefaultConfig(), nil)

			ctx := context.Background()
			tree, err := BuildTree(ctx, h.d)
			if err != nil {
				t.Fatal(err)
			}
			var walk func([]Node)
			walk = func(ns []Node) {
				for i := range ns {
					if ns[i].Name == OrphanDir {
						t.Errorf("orphan folder %d leaked into the review tree", ns[i].ID)
					}
					walk(ns[i].Children)
				}
			}
			walk(tree)

			// the orphan row still exists — Confirm just never got asked about it
			if err := Confirm(ctx, h.d, tree); err != nil {
				t.Fatal(err)
			}
			var status string
			if err := h.d.SQL.Get(&status,
				`SELECT status FROM virtual_fs_entries WHERE target_path = ?`, OrphanDir+"/IMG_0042.AAE"); err != nil {
				t.Fatal(err)
			}
			if status != db.StatusApproved {
				t.Errorf("orphan row status = %q, want %q (approved along with everything else)", status, db.StatusApproved)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

// dropNodeByID removes a node from the tree entirely, the way the review TUI's
// merge does once it has folded that node into a sibling.
func dropNodeByID(nodes []Node, id int64) []Node {
	out := nodes[:0]
	for _, n := range nodes {
		if n.ID == id {
			continue
		}
		n.Children = dropNodeByID(n.Children, id)
		out = append(out, n)
	}
	return out
}

func TestConfigForNoneSentinel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		groupBy []string
		want    []string
	}{
		{"none sentinel", []string{RuleNone}, nil},
		{"empty keeps defaults", nil, DefaultConfig().Rules},
		{"explicit levels", []string{RuleMedia}, []string{RuleMedia}},
	} {
		got := ConfigFor(&config.Configuration{Rules: tc.groupBy}).Rules
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: Rules = %v, want %v", tc.name, got, tc.want)
		}
	}
	// a nil app config is DefaultConfig, not a panic
	if got := ConfigFor(nil).Rules; !slices.Equal(got, DefaultConfig().Rules) {
		t.Errorf("ConfigFor(nil).Rules = %v, want DefaultConfig's levels", got)
	}
}

// A file already in the library holds its name: a rename that lands a new file
// on it gets the next _N, compared NFC- and case-insensitively, the same rule
// buildTargets follows.
func TestReviewConfirmAvoidsPlacedNames(t *testing.T) {
	h := newHarness(t)
	fresh := h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
	placed := h.addFile(t, "lib/a.heic", "IMAGE", metaWith("2024:06:20 14:00:00", 0, 0, 3024, 4032))
	ctx := context.Background()
	if _, err := h.d.ExecContext(ctx, `UPDATE file_registry SET placed = 1 WHERE id = ?`, placed); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedEntry(t, h.d, placed, "lib/a.heic", "2024/06_June/Manali/a.heic", db.StatusDone)

	cfg := DefaultConfig()
	cfg.Rules = []string{RuleLocation}
	h.build(t, cfg, installtest.Resolver(t))

	tree, err := BuildTree(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := renameFirstLeaf(tree, "Manali"); !ok {
		t.Fatal("no leaf to rename")
	}
	if err := Confirm(ctx, h.d, tree); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	var got string
	if err := h.d.SQL.Get(&got, `SELECT target_path FROM virtual_fs_entries WHERE file_id = ?`, fresh); err != nil {
		t.Fatal(err)
	}
	if want := "2024/06_June/Manali/A_2.HEIC"; got != want {
		t.Errorf("target_path = %q, want %q (a.heic is already placed there)", got, want)
	}
}

// A review rename landing a photo on a placed name moves its edit to the same
// _N, even though the edit's own name was free.
func TestReviewConfirmKeepsPairSuffix(t *testing.T) {
	h := newHarness(t)
	photo := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
	edit := h.addFile(t, "dump/IMG_0001.AAE", classifier.MediaTypeSidecar, classifier.CommonMetadata{})
	placed := h.addFile(t, "lib/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:20 14:00:00", 0, 0, 3024, 4032))
	ctx := context.Background()
	if _, err := h.d.ExecContext(ctx, `UPDATE file_registry SET placed = 1 WHERE id = ?`, placed); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedEntry(t, h.d, placed, "lib/IMG_0001.HEIC", "2024/06_June/Manali/IMG_0001.HEIC", db.StatusDone)

	cfg := DefaultConfig()
	cfg.Rules = []string{RuleLocation}
	h.build(t, cfg, installtest.Resolver(t))

	tree, err := BuildTree(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := renameFirstLeaf(tree, "Manali"); !ok {
		t.Fatal("no leaf to rename")
	}
	if err := Confirm(ctx, h.d, tree); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	for id, want := range map[int64]string{
		photo: "2024/06_June/Manali/IMG_0001_2.HEIC",
		edit:  "2024/06_June/Manali/IMG_0001_2.AAE",
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
