// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
)

// renameFirstSuggested edits the first node carrying suggestions (the location
// node) and returns its immutable ID so the test can assert on the rewrite.
func renameFirstSuggested(nodes []Node, newName string) (oldID string, ok bool) {
	for i := range nodes {
		if len(nodes[i].Suggestions) > 0 {
			oldID = nodes[i].ID
			nodes[i].Name = newName
			return oldID, true
		}
		if oldID, ok = renameFirstSuggested(nodes[i].Children, newName); ok {
			return oldID, true
		}
	}
	return "", false
}

func TestReview(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"ReviewBuildAndConfirm", func(t *testing.T) {
			h := newHarness(t)
			// two unlocated files, same folder + day → one cluster, one location node
			// that carries a suggestion (the node a reviewer renames)
			h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
			h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:03 16:00:00", 0, 0, 3024, 4032))
			cfg := DefaultConfig()
			cfg.Rules = []string{RuleLocation} // eventSegment suggestion rung needs no date level ahead of it
			h.build(t, cfg, &fakeGeo{cities: map[int]string{}})

			ctx := context.Background()
			tree, err := BuildTree(ctx, h.sessionID, h.d)
			if err != nil {
				t.Fatal(err)
			}
			if len(tree) == 0 {
				t.Fatal("empty tree")
			}

			oldID, ok := renameFirstSuggested(tree, "Manali")
			if !ok {
				t.Fatal("no node carried a suggestion to rename")
			}

			if err := Confirm(ctx, h.sessionID, h.d, tree); err != nil {
				t.Fatal(err)
			}

			var rows []struct {
				TargetPath string `db:"target_path"`
				Status     string `db:"status"`
			}
			if err := h.d.SQL.Select(&rows,
				`SELECT target_path, status FROM virtual_fs_entries WHERE session_id = ?`,
				h.sessionID.String()); err != nil {
				t.Fatal(err)
			}
			oldSeg := "/" + path.Base(oldID) + "/"
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

			// the rename is remembered as an EVENT label with a capture-time span
			var labels []struct {
				Label     string  `db:"label"`
				Kind      string  `db:"kind"`
				TimeStart *string `db:"time_start"`
			}
			if err := h.d.SQL.Select(&labels,
				`SELECT label, kind, time_start FROM user_labels`); err != nil {
				t.Fatal(err)
			}
			if len(labels) != 1 || labels[0].Label != "Manali" || labels[0].Kind != "EVENT" {
				t.Fatalf("labels = %+v, want one EVENT Manali", labels)
			}
			if labels[0].TimeStart == nil {
				t.Error("EVENT label has no time span")
			}
		}},
		{"ReviewConfirmRejectsUnknownID", func(t *testing.T) {
			h := newHarness(t)
			h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
			h.build(t, DefaultConfig(), &fakeGeo{cities: map[int]string{}})

			bogus := []Node{{ID: "not/a/real/path", Name: "x", Children: []Node{}}}
			if err := Confirm(context.Background(), h.sessionID, h.d, bogus); err == nil {
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
			cfg.Rules = []string{RuleLocation} // eventSegment suggestion rung needs no date level ahead of it
			h.build(t, cfg, &fakeGeo{cities: map[int]string{}})

			ctx := context.Background()
			tree, err := BuildTree(ctx, h.sessionID, h.d)
			if err != nil {
				t.Fatal(err)
			}
			var suggested []*Node
			var collect func(nodes []Node)
			collect = func(nodes []Node) {
				for i := range nodes {
					if len(nodes[i].Suggestions) > 0 {
						suggested = append(suggested, &nodes[i])
					}
					collect(nodes[i].Children)
				}
			}
			collect(tree)
			if len(suggested) < 2 {
				t.Fatalf("want two sibling suggestion nodes, got %d", len(suggested))
			}
			suggested[0].Name = "Manali"
			suggested[1].Name = "Manali"

			if err := Confirm(ctx, h.sessionID, h.d, tree); err != nil {
				t.Fatalf("Confirm merge: %v", err)
			}

			var targets []string
			if err := h.d.SQL.Select(&targets,
				`SELECT DISTINCT target_path FROM virtual_fs_entries WHERE session_id = ?`, h.sessionID.String()); err != nil {
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
			cfg.Rules = []string{RuleLocation} // eventSegment suggestion rung needs no date level ahead of it
			h.build(t, cfg, &fakeGeo{cities: map[int]string{}})

			ctx := context.Background()
			tree, err := BuildTree(ctx, h.sessionID, h.d)
			if err != nil {
				t.Fatal(err)
			}
			var suggested []*Node
			var collect func(nodes []Node)
			collect = func(nodes []Node) {
				for i := range nodes {
					if len(nodes[i].Suggestions) > 0 {
						suggested = append(suggested, &nodes[i])
					}
					collect(nodes[i].Children)
				}
			}
			collect(tree)
			if len(suggested) < 2 {
				t.Fatalf("want two sibling suggestion nodes, got %d", len(suggested))
			}
			foldedID := suggested[1].ID
			suggested[0].Name = "Manali"
			suggested[0].MergedIDs = []string{foldedID}
			tree = dropNodeByID(tree, foldedID)

			if err := Confirm(ctx, h.sessionID, h.d, tree); err != nil {
				t.Fatalf("Confirm merge: %v", err)
			}

			var targets []string
			if err := h.d.SQL.Select(&targets,
				`SELECT target_path FROM virtual_fs_entries WHERE session_id = ?`, h.sessionID.String()); err != nil {
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
		{"ProposalSessionEmptyDB", func(t *testing.T) {
			h := newHarness(t)
			if _, err := ProposalSession(context.Background(), h.d); !errors.Is(err, ErrNoProposal) {
				t.Fatalf("err = %v, want ErrNoProposal", err)
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
			cfg.Rules = []string{RuleLocation} // eventSegment suggestion rung needs no date level ahead of it
			h.build(t, cfg, nil)

			ctx := context.Background()
			tree, err := BuildTree(ctx, h.sessionID, h.d)
			if err != nil {
				t.Fatal(err)
			}
			var suggested []*Node
			var collect func([]Node)
			collect = func(ns []Node) {
				for i := range ns {
					if len(ns[i].Suggestions) > 0 {
						suggested = append(suggested, &ns[i])
					}
					collect(ns[i].Children)
				}
			}
			collect(tree)
			if len(suggested) != 2 {
				t.Fatalf("want 2 renameable dirs, got %d", len(suggested))
			}
			// merge them onto one folder — both files now want the same basename
			suggested[0].Name, suggested[1].Name = "Manali", "Manali"

			if err := Confirm(ctx, h.sessionID, h.d, tree); err != nil {
				t.Fatalf("Confirm: %v", err)
			}

			var targets []string
			if err := h.d.SQL.Select(&targets,
				`SELECT target_path FROM virtual_fs_entries WHERE session_id = ? ORDER BY target_path`,
				h.sessionID.String()); err != nil {
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
			cfg.Rules = []string{RuleLocation} // eventSegment suggestion rung needs no date level ahead of it
			h.build(t, cfg, nil)

			ctx := context.Background()
			tree, err := BuildTree(ctx, h.sessionID, h.d)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := renameFirstSuggested(tree, "Goa [2024]"); !ok {
				t.Fatal("no renameable node in the proposal")
			}
			if err := Confirm(ctx, h.sessionID, h.d, tree); err != nil {
				t.Fatal(err)
			}

			tree, err = BuildTree(ctx, h.sessionID, h.d)
			if err != nil {
				t.Fatal(err)
			}
			var bracketed string
			var walk func([]Node)
			walk = func(ns []Node) {
				for i := range ns {
					if strings.Contains(ns[i].ID, "[2024]") {
						bracketed = ns[i].ID
					}
					walk(ns[i].Children)
				}
			}
			walk(tree)
			if bracketed == "" {
				t.Fatal("rename to a bracketed name did not reach target_path")
			}

			files, err := FilesUnder(ctx, h.sessionID, bracketed, h.d)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 1 {
				t.Errorf("FilesUnder(%q) = %v, want the one file under it", bracketed, files)
			}
		}},
		// TestSuggestionEqualToFolderNameIsDropped covers the noise case: a source
		// folder named after the camera makes SOURCE_FOLDER suggest the name the
		// folder already has ("Canon EOS 700D (suggested: Canon EOS 700D)"). Offering
		// the reviewer a rename to the current name is not a suggestion.
		{"SuggestionEqualToFolderNameIsDropped", func(t *testing.T) {
			h := newHarness(t)
			h.addFile(t, "Canon EOS 700D/DSC_0001.JPG", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 4000, 3000))
			h.build(t, DefaultConfig(), &fakeGeo{cities: map[int]string{}})

			ctx := context.Background()
			tree, err := BuildTree(ctx, h.sessionID, h.d)
			if err != nil {
				t.Fatal(err)
			}
			var walk func(nodes []Node)
			walk = func(nodes []Node) {
				for _, n := range nodes {
					for _, s := range n.Suggestions {
						if s.Name == n.Name {
							t.Errorf("node %q offers a suggestion of its own name (%s)", n.ID, s.Source)
						}
					}
					walk(n.Children)
				}
			}
			walk(tree)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

// dropNodeByID removes a node from the tree entirely, the way the review TUI's
// merge does once it has folded that node into a sibling.
func dropNodeByID(nodes []Node, id string) []Node {
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
