package vfs

import (
	"context"
	"errors"
	"path"
	"strings"
	"testing"

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

func TestReviewBuildAndConfirm(t *testing.T) {
	h := newHarness(t)
	// two unlocated files, same folder + day → one cluster, one location node
	// that carries a suggestion (the node a reviewer renames)
	h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
	h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:03 16:00:00", 0, 0, 3024, 4032))
	h.build(t, DefaultConfig(), &fakeGeo{cities: map[int]string{}})

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
}

func TestReviewConfirmRejectsUnknownID(t *testing.T) {
	h := newHarness(t)
	h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
	h.build(t, DefaultConfig(), &fakeGeo{cities: map[int]string{}})

	bogus := []Node{{ID: "not/a/real/path", Name: "x", Children: []Node{}}}
	if err := Confirm(context.Background(), h.sessionID, h.d, bogus); err == nil {
		t.Fatal("expected error for unknown node id")
	}
}

// TestReviewConfirmMergesCollidingRenames covers the real case a user hit:
// two separate unresolved date clusters turn out to be the same place.
// Renaming both to the same name is a deliberate merge, not an error — both
// dirs collapse onto one final path and get one deduped EVENT label.
func TestReviewConfirmMergesCollidingRenames(t *testing.T) {
	h := newHarness(t)
	// two unlocated clusters days apart → two sibling event dirs under June
	h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
	h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:20 14:00:00", 0, 0, 3024, 4032))
	h.build(t, DefaultConfig(), &fakeGeo{cities: map[int]string{}})

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

// TestReviewConfirmRemapsMergedIDs covers the review TUI's merge: the folded-
// away node is gone from the submitted tree, so MergedIDs on the survivor is
// the only thing telling Confirm its files still need remapping — without it
// they'd silently keep their old target_path.
func TestReviewConfirmRemapsMergedIDs(t *testing.T) {
	h := newHarness(t)
	h.addFile(t, "dump/A.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
	h.addFile(t, "dump/B.HEIC", "IMAGE", metaWith("2024:06:20 14:00:00", 0, 0, 3024, 4032))
	h.build(t, DefaultConfig(), &fakeGeo{cities: map[int]string{}})

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
}

func TestProposalSessionEmptyDB(t *testing.T) {
	h := newHarness(t)
	if _, err := ProposalSession(context.Background(), h.d); !errors.Is(err, ErrNoProposal) {
		t.Fatalf("err = %v, want ErrNoProposal", err)
	}
}
