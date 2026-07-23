package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/location"
)

func sampleTree() []vfs.Node {
	return []vfs.Node{{ID: "2024", Name: "2024", Children: []vfs.Node{
		{ID: "2024/June", Name: "June", FileCount: 1},
	}}}
}

// TestRelayoutDoneRebuildsRowsAndResetsState covers the [L] key's async
// result: a successful relayout replaces the tree/rows, resets navigation
// and any in-progress merge selection (their old row pointers are gone), and
// reports which preset just applied.
func TestRelayoutDoneRebuildsRowsAndResetsState(t *testing.T) {
	m := newReviewModel(sampleTree(), nil, nil, uuid.Nil, nil, nil)
	m.cursor, m.offset = 5, 2
	m.visualMode, m.lastMergeTree = true, []vfs.Node{{ID: "stale"}}
	m.relayouting = true
	m.layoutIdx = 1

	newTree := []vfs.Node{{ID: "2025", Name: "2025", FileCount: 3}}
	next, _ := m.Update(relayoutDoneMsg{tree: newTree})
	rm := next.(reviewModel)

	if rm.relayouting {
		t.Error("relayouting still true after relayoutDoneMsg")
	}
	if len(rm.rows) != 1 || rm.rows[0].node.ID != "2025" {
		t.Fatalf("rows = %+v, want rebuilt from newTree", rm.rows)
	}
	if rm.cursor != 0 || rm.offset != 0 {
		t.Errorf("cursor/offset = %d/%d, want reset to 0/0", rm.cursor, rm.offset)
	}
	if rm.visualMode || rm.lastMergeTree != nil {
		t.Error("visual selection / pending undo should be cleared after a relayout")
	}
	if rm.statusMsg == "" {
		t.Error("expected a status message naming the new layout")
	}
}

// TestRelayoutDoneErrorKeepsOldTree covers the failure path: an error must
// not clobber whatever the reviewer was already looking at.
func TestRelayoutDoneErrorKeepsOldTree(t *testing.T) {
	m := newReviewModel(sampleTree(), nil, nil, uuid.Nil, nil, nil)
	m.relayouting = true
	origRows := m.rows

	next, _ := m.Update(relayoutDoneMsg{err: errors.New("boom")})
	rm := next.(reviewModel)

	if rm.relayouting {
		t.Error("relayouting still true after an errored relayoutDoneMsg")
	}
	if rm.relayoutErr == nil {
		t.Error("expected relayoutErr to be set")
	}
	if len(rm.rows) != len(origRows) {
		t.Errorf("rows changed on error: got %d, want %d (unchanged)", len(rm.rows), len(origRows))
	}
}

// TestLayoutKeyCyclesPresetsAndKicksOffRelayout covers [L]: it advances to
// the next preset and starts an async rebuild, as long as a resolver is
// available (the zero-value *location.Resolver here is never actually
// invoked — Update only returns the Cmd, it doesn't run it).
func TestLayoutKeyCyclesPresetsAndKicksOffRelayout(t *testing.T) {
	m := newReviewModel(sampleTree(), nil, nil, uuid.Nil, &location.Resolver{}, nil)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("L")})
	rm := next.(reviewModel)

	if rm.layoutIdx != 1 {
		t.Errorf("layoutIdx = %d, want 1 after first [L]", rm.layoutIdx)
	}
	if !rm.relayouting {
		t.Error("expected relayouting = true right after pressing [L]")
	}
	if cmd == nil {
		t.Error("expected a non-nil Cmd to kick off the relayout")
	}
}

// TestLayoutKeyNoopsWithoutResolver covers the guard: pressing [L] before a
// location resolver is available must not start a relayout that would strip
// every file's location.
func TestLayoutKeyNoopsWithoutResolver(t *testing.T) {
	m := newReviewModel(sampleTree(), nil, nil, uuid.Nil, nil, nil)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("L")})
	rm := next.(reviewModel)

	if rm.layoutIdx != 0 || rm.relayouting {
		t.Error("expected [L] to no-op without a resolver")
	}
	if cmd != nil {
		t.Error("expected no Cmd without a resolver")
	}
}

// TestPressingPAlwaysDispatchesAsync covers that "p" always kicks off
// peekCmd — the cache check now happens inside it (it needs the file list to
// compute a signature), not synchronously in the key handler.
func TestPressingPAlwaysDispatchesAsync(t *testing.T) {
	m := newReviewModel(sampleTree(), nil, nil, uuid.Nil, nil, nil)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	rm := next.(reviewModel)

	if !rm.previewing {
		t.Error("expected previewing = true right after pressing p")
	}
	if cmd == nil {
		t.Error("expected a non-nil Cmd")
	}
}

// TestPreviewDoneCachesBySignature covers the write side of the cache: a
// successful copy is remembered under its file-membership signature, not the
// node it happened to be peeked from.
func TestPreviewDoneCachesBySignature(t *testing.T) {
	m := newReviewModel(sampleTree(), nil, nil, uuid.Nil, nil, nil)

	next, _ := m.Update(previewDoneMsg{signature: "a.jpg\x00b.jpg", dir: "/tmp/wandersort-preview-xyz"})
	rm := next.(reviewModel)

	if rm.previewDirs["a.jpg\x00b.jpg"] != "/tmp/wandersort-preview-xyz" {
		t.Errorf("previewDirs = %+v, want the copied dir cached under the signature", rm.previewDirs)
	}
}

// TestFilesSignatureDedupesParentAndLeafNode covers the actual reported bug:
// a folder with one child chain (e.g. .../08/Horizontal/Photos) and its leaf
// both cover the exact same underlying files — peeking either must resolve
// to the same signature so the same temp copy gets reused.
func TestFilesSignatureDedupesParentAndLeafNode(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	sessionID := dbtest.NewSession(t, d, db.StatusScored)

	for i, name := range []string{"a.jpg", "b.jpg"} {
		fileID := int64(i + 1)
		if _, err := d.ExecContext(ctx, `
			INSERT INTO file_registry (id, file_dir, file_name, file_size, file_modified_at,
				scan_session_id, file_extension, media_type, discovered_at, last_seen_at)
			VALUES (?, '/src', ?, 1024, '2024-06-01T10:00:00.000000000Z', ?, '.jpg', 'IMAGE',
				'2024-06-01T10:00:00.000000000Z', '2024-06-01T10:00:00.000000000Z')`,
			fileID, name, sessionID.String()); err != nil {
			t.Fatal(err)
		}
		target := "2017/April/08/Horizontal/Photos/" + name
		if _, err := d.ExecContext(ctx, `
			INSERT INTO virtual_fs_entries (session_id, file_id, source_path, target_path, status)
			VALUES (?, ?, ?, ?, 'PROPOSED')`,
			sessionID.String(), fileID, "/src/"+name, target); err != nil {
			t.Fatal(err)
		}
	}

	parentFiles, err := vfs.FilesUnder(ctx, sessionID, "2017/April/08", d)
	if err != nil {
		t.Fatal(err)
	}
	leafFiles, err := vfs.FilesUnder(ctx, sessionID, "2017/April/08/Horizontal/Photos", d)
	if err != nil {
		t.Fatal(err)
	}

	if len(parentFiles) != 2 || len(leafFiles) != 2 {
		t.Fatalf("parentFiles = %v, leafFiles = %v, want 2 files each", parentFiles, leafFiles)
	}
	if filesSignature(parentFiles) != filesSignature(leafFiles) {
		t.Errorf("signatures differ: parent %q vs leaf %q, want equal so both peeks share one temp copy",
			filesSignature(parentFiles), filesSignature(leafFiles))
	}
}

// TestCleanupPreviewDirsRemovesEverything covers the exit-time sweep: every
// temp dir a review session created must be gone once it's over, regardless
// of how the reviewer exited (confirm, quit, ctrl-c all funnel through this).
func TestCleanupPreviewDirsRemovesEverything(t *testing.T) {
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	for _, dir := range []string{a, b} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cleanupPreviewDirs(map[string]string{"nodeA": a, "nodeB": b})

	for _, dir := range []string{a, b} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s still exists after cleanupPreviewDirs", dir)
		}
	}
}

// siblingTree has two true siblings ("03", "09") under the same parent
// ("June"), the shape a real merge (two date-fallback clusters that turn out
// to be the same place) actually happens on.
func siblingTree() []vfs.Node {
	return []vfs.Node{{ID: "2024", Name: "2024", Children: []vfs.Node{
		{ID: "2024/June", Name: "June", Children: []vfs.Node{
			{ID: "2024/June/03", Name: "03", FileCount: 1},
			{ID: "2024/June/09", Name: "09", FileCount: 1},
		}},
	}}}
}

// TestMergeWithoutVisualModeIsRejectedLoudly covers the reported "merge
// doesn't work" complaint: pressing m without having pressed V first (e.g.
// typed lowercase v, which matches no keybinding) must be an obvious warning,
// not a message indistinguishable from routine dim status text.
func TestMergeWithoutVisualModeIsRejectedLoudly(t *testing.T) {
	m := newReviewModel(siblingTree(), nil, nil, uuid.Nil, nil, nil)

	m.mergeSelection()

	if m.statusMsg == "" || !m.statusIsErr {
		t.Errorf("statusMsg = %q, statusIsErr = %v, want a flagged error asking to press V first", m.statusMsg, m.statusIsErr)
	}
}

// TestMergeSingleRowIsRejected covers pressing m right after V with no
// cursor movement — only one row "selected", nothing to merge.
func TestMergeSingleRowIsRejected(t *testing.T) {
	m := newReviewModel(siblingTree(), nil, nil, uuid.Nil, nil, nil)
	m.visualMode = true
	m.visualAnchor = m.cursor // no movement — selection is just the current row

	m.mergeSelection()

	if m.statusMsg == "" || !m.statusIsErr {
		t.Errorf("statusMsg = %q, statusIsErr = %v, want a flagged error", m.statusMsg, m.statusIsErr)
	}
}

// TestMergeSelectingOnlyStructuralRowsIsRejected covers selecting rows that
// still have children (Year/Month rows, not leaves) — nothing to merge since
// only leaves are merge candidates.
func TestMergeSelectingOnlyStructuralRowsIsRejected(t *testing.T) {
	m := newReviewModel(siblingTree(), nil, nil, uuid.Nil, nil, nil)
	// row 0 = "2024" (has children), row 1 = "June" (has children) — no leaves
	m.visualMode = true
	m.visualAnchor = 0
	m.cursor = 1

	m.mergeSelection()

	if m.statusMsg == "" || !m.statusIsErr {
		t.Errorf("statusMsg = %q, statusIsErr = %v, want a flagged error", m.statusMsg, m.statusIsErr)
	}
}

// nodeByID finds a row by its node ID, however the merge reshuffled row order.
func nodeByID(rows []*reviewRow, id string) *reviewRow {
	for _, r := range rows {
		if r.node.ID == id {
			return r
		}
	}
	return nil
}

// TestMergeSiblingsSucceeds covers the simplest working case: two true
// siblings selected with V then merged with m (their lowest common ancestor
// is the parent they're already under, so this is a same-parent merge).
func TestMergeSiblingsSucceeds(t *testing.T) {
	m := newReviewModel(siblingTree(), nil, nil, uuid.Nil, nil, nil)
	// rows: 0=2024, 1=June, 2=03, 3=09 — select rows 2 and 3
	m.visualMode = true
	m.visualAnchor = 2
	m.cursor = 3

	m.mergeSelection()

	if m.statusIsErr {
		t.Fatalf("expected success, got error status: %q", m.statusMsg)
	}
	r03 := nodeByID(m.rows, "2024/June/03")
	if r03 == nil || r03.newName != "03" {
		t.Fatalf("want surviving node renamed to %q, got %+v", "03", r03)
	}
	if r09 := nodeByID(m.rows, "2024/June/09"); r09 != nil {
		t.Error("09 should be folded into 03, not still its own row")
	}
	if got := r03.node.MergedIDs; len(got) != 1 || got[0] != "2024/June/09" {
		t.Errorf("MergedIDs = %v, want [2024/June/09] so Confirm remaps its files too", got)
	}
	if r03.node.FileCount != 2 {
		t.Errorf("merged node FileCount = %d, want 2 (both leaves' files)", r03.node.FileCount)
	}
	if m.visualMode {
		t.Error("visualMode should be cleared after a merge")
	}
	if m.lastMergeTree == nil {
		t.Error("expected lastMergeTree to be set for undo")
	}
}

// crossBranchTree mirrors the real reported case: the same device's photos
// spread across three different months, each its own single-file leaf.
func crossBranchTree() []vfs.Node {
	leaf := func(id, name string) vfs.Node { return vfs.Node{ID: id, Name: name, FileCount: 1} }
	return []vfs.Node{{ID: "2017", Name: "2017", Children: []vfs.Node{
		{ID: "2017/April", Name: "April", Children: []vfs.Node{
			{ID: "2017/April/20", Name: "20", Children: []vfs.Node{
				leaf("2017/April/20/Canon EOS 700D", "Canon EOS 700D"),
			}},
		}},
		{ID: "2017/August", Name: "August", Children: []vfs.Node{
			{ID: "2017/August/15", Name: "15", Children: []vfs.Node{
				leaf("2017/August/15/Canon EOS 700D", "Canon EOS 700D"),
			}},
		}},
		{ID: "2017/October", Name: "October", Children: []vfs.Node{
			{ID: "2017/October/19", Name: "19", Children: []vfs.Node{
				leaf("2017/October/19/Canon EOS 700D", "Canon EOS 700D"),
			}},
		}},
	}}}
}

// TestMergeAcrossBranchesCollapsesToOneNode covers the real reported case:
// merging the same camera's leaves out of three different Month/Day branches
// must leave exactly ONE folder under the Year holding all the files — not
// three same-named siblings next to three now-empty Month/Day chains, which
// is what the reviewer sees as "merge didn't work" even though Confirm would
// have collapsed the paths later.
func TestMergeAcrossBranchesCollapsesToOneNode(t *testing.T) {
	m := newReviewModel(crossBranchTree(), nil, nil, uuid.Nil, nil, nil)
	// flattened order: 2017, April, 20, Canon(April), August, 15, Canon(August), October, 19, Canon(October)
	// select from April's Canon leaf through October's Canon leaf — spans
	// several structural (non-leaf) rows in between, which must be ignored
	m.visualAnchor = 3
	m.cursor = 9
	m.visualMode = true

	m.mergeSelection()

	if m.statusIsErr {
		t.Fatalf("expected success, got error status: %q", m.statusMsg)
	}

	year := findNodeByID(m.tree, "2017")
	if year == nil {
		t.Fatal("2017 node missing from tree")
	}
	if len(year.Children) != 1 {
		t.Fatalf("2017 has %d children, want 1 (every emptied Month chain pruned, all leaves folded into one)", len(year.Children))
	}
	canon := year.Children[0]
	if canon.Name != "Canon EOS 700D" || canon.FileCount != 3 {
		t.Errorf("merged child = %q with %d files, want %q with 3", canon.Name, canon.FileCount, "Canon EOS 700D")
	}
	if len(canon.MergedIDs) != 2 {
		t.Errorf("MergedIDs = %v, want the two folded-away leaf IDs so Confirm remaps their files", canon.MergedIDs)
	}
	if year.FileCount != 3 {
		t.Errorf("2017 FileCount = %d, want 3", year.FileCount)
	}
	for _, month := range []string{"April", "August", "October", "April/20"} {
		if n := findNodeByID(m.tree, "2017/"+month); n != nil {
			t.Errorf("%s should have been pruned — nothing left under it", month)
		}
	}
}

// TestMergeAcrossBranchesRejectsWithNoCommonAncestor covers selecting leaves
// that share literally no ancestor (different years) — nothing sensible to
// reparent under.
func TestMergeAcrossBranchesRejectsWithNoCommonAncestor(t *testing.T) {
	tree := []vfs.Node{
		{ID: "2017", Name: "2017", Children: []vfs.Node{{ID: "2017/Camera", Name: "Camera", FileCount: 1}}},
		{ID: "2018", Name: "2018", Children: []vfs.Node{{ID: "2018/Camera", Name: "Camera", FileCount: 1}}},
	}
	m := newReviewModel(tree, nil, nil, uuid.Nil, nil, nil)
	// rows: 0=2017, 1=2017/Camera, 2=2018, 3=2018/Camera
	m.visualAnchor = 1
	m.cursor = 3
	m.visualMode = true

	m.mergeSelection()

	if !m.statusIsErr {
		t.Errorf("expected rejection for leaves with no common ancestor, got success: %q", m.statusMsg)
	}
}

// TestUndoRestoresTreeAfterCrossBranchMerge covers [u] on the new structural
// merge: it must restore the whole pre-merge tree (the old per-row newName
// undo can't undo a reparent), and only once (no redo).
func TestUndoRestoresTreeAfterCrossBranchMerge(t *testing.T) {
	m := newReviewModel(crossBranchTree(), nil, nil, uuid.Nil, nil, nil)
	m.visualAnchor = 3
	m.cursor = 9
	m.visualMode = true
	m.mergeSelection()
	if m.statusIsErr {
		t.Fatalf("merge failed: %q", m.statusMsg)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	rm := next.(reviewModel)

	april20 := findNodeByID(rm.tree, "2017/April/20")
	if april20 == nil || len(april20.Children) != 1 || april20.Children[0].Name != "Canon EOS 700D" {
		t.Errorf("2017/April/20 should have its Canon EOS 700D leaf back after undo, got %+v", april20)
	}
	year := findNodeByID(rm.tree, "2017")
	for _, c := range year.Children {
		if c.Name == "Canon EOS 700D" {
			t.Error("2017 should not have a direct Canon EOS 700D child after undo")
		}
	}
	if rm.lastMergeTree != nil {
		t.Error("lastMergeTree should be cleared after undo (no redo)")
	}

	// pressing u again with nothing pending must be a no-op, not a crash
	next2, _ := rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	rm2 := next2.(reviewModel)
	if rm2.lastMergeTree != nil {
		t.Error("second undo should still be a no-op")
	}
}
