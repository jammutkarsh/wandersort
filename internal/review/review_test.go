// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// TestMain keeps every test in this package off the real preview root: a
// finished review deletes it, and a test run must never take a developer's own
// preview copies with it.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "wandersort-previews-test-*")
	if err != nil {
		panic(err)
	}
	previewRootDir = filepath.Join(dir, "previews")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// usePreviewRoot points the preview root at a directory only this test uses.
func usePreviewRoot(t *testing.T) string {
	t.Helper()
	prev := previewRootDir
	previewRootDir = filepath.Join(t.TempDir(), "previews")
	t.Cleanup(func() { previewRootDir = prev })
	if err := os.MkdirAll(previewRootDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return previewRootDir
}

func sampleTree() []vfs.Node {
	return []vfs.Node{{ID: pid("2024"), Name: "2024", Children: []vfs.Node{
		{ID: pid("2024/June"), Name: "June", FileCount: 1},
	}}}
}

func TestReview(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// TestPressingPAlwaysDispatchesAsync covers that "p" always kicks off
		// peekCmd — the cache check now happens inside it (it needs the file list to
		// compute a signature), not synchronously in the key handler.
		{"PressingPAlwaysDispatchesAsync", func(t *testing.T) {
			m := newModel(sampleTree(), nil, nil, nil, nil, nil, t.TempDir())

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
			rm := next.(Model)

			if !rm.previewing {
				t.Error("expected previewing = true right after pressing p")
			}
			if cmd == nil {
				t.Error("expected a non-nil Cmd")
			}
		}},
		// TestPreviewDirDedupesParentAndLeafNode covers the actual reported bug:
		// a folder with one child chain (e.g. .../08/Horizontal/Photos) and its leaf
		// both cover the exact same underlying files — peeking either must resolve
		// to the same preview dir so the same temp copy gets reused.
		{"PreviewDirDedupesParentAndLeafNode", func(t *testing.T) {
			ctx := context.Background()
			d := dbtest.New(t)

			for i, name := range []string{"a.jpg", "b.jpg"} {
				fileID := int64(i + 1)
				if _, err := d.ExecContext(ctx, `
			INSERT INTO file_registry (id, file_dir, file_name, file_size, file_modified_at,
				file_extension, media_type, discovered_at, last_seen_at)
			VALUES (?, '/src', ?, 1024, '2024-06-01T10:00:00.000000000Z', '.jpg', 'IMAGE',
				'2024-06-01T10:00:00.000000000Z', '2024-06-01T10:00:00.000000000Z')`,
					fileID, name); err != nil {
					t.Fatal(err)
				}
				target := "2017/April/08/Horizontal/Photos/" + name
				dbtest.SeedEntry(t, d, fileID, "/src/"+name, target, db.StatusProposed)
			}

			parentFiles, err := vfs.FilesUnder(ctx, nodeAt(t, d, "2017/April/08"), d)
			if err != nil {
				t.Fatal(err)
			}
			leafFiles, err := vfs.FilesUnder(ctx, nodeAt(t, d, "2017/April/08/Horizontal/Photos"), d)
			if err != nil {
				t.Fatal(err)
			}

			if len(parentFiles) != 2 || len(leafFiles) != 2 {
				t.Fatalf("parentFiles = %v, leafFiles = %v, want 2 files each", parentFiles, leafFiles)
			}
			if previewDirFor(parentFiles) != previewDirFor(leafFiles) {
				t.Errorf("preview dirs differ: parent %q vs leaf %q, want equal so both peeks share one temp copy",
					previewDirFor(parentFiles), previewDirFor(leafFiles))
			}
		}},
		// TestCleanPreviewsRemovesEverything covers the sweep a saved plan and
		// `wandersort reset` both run: copies survive an unsaved exit, but once the
		// plan is written there is nothing left to peek at.
		{"CleanPreviewsRemovesEverything", func(t *testing.T) {
			root := usePreviewRoot(t)
			for _, name := range []string{"a", "b"} {
				if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			if err := CleanPreviews(); err != nil {
				t.Fatal(err)
			}

			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Errorf("%s still exists after CleanPreviews", root)
			}
		}},
		// TestMergeWithoutVisualModeIsRejectedLoudly covers the reported "merge
		// doesn't work" complaint: pressing m without having pressed V first (e.g.
		// typed lowercase v, which matches no keybinding) must be an obvious warning,
		// not a message indistinguishable from routine dim status text.
		{"MergeWithoutVisualModeIsRejectedLoudly", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil, nil, t.TempDir())

			m.mergeSelection()

			if m.statusMsg == "" || !m.statusIsErr {
				t.Errorf("statusMsg = %q, statusIsErr = %v, want a flagged error asking to press V first", m.statusMsg, m.statusIsErr)
			}
		}},
		// TestMergeSingleRowIsRejected covers pressing m right after V with no
		// cursor movement — only one row "selected", nothing to merge.
		{"MergeSingleRowIsRejected", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.visualMode = true
			m.visualAnchor = m.cursor // no movement — selection is just the current row

			m.mergeSelection()

			if m.statusMsg == "" || !m.statusIsErr {
				t.Errorf("statusMsg = %q, statusIsErr = %v, want a flagged error", m.statusMsg, m.statusIsErr)
			}
		}},
		// TestMergeSelectingOnlyStructuralRowsIsRejected covers selecting rows that
		// still have children (Year/Month rows, not leaves) — nothing to merge since
		// only leaves are merge candidates.
		{"MergeSelectingOnlyStructuralRowsIsRejected", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil, nil, t.TempDir())
			// row 0 = "2024" (has children), row 1 = "June" (has children) — no leaves
			m.visualMode = true
			m.visualAnchor = 0
			m.cursor = 1

			m.mergeSelection()

			if m.statusMsg == "" || !m.statusIsErr {
				t.Errorf("statusMsg = %q, statusIsErr = %v, want a flagged error", m.statusMsg, m.statusIsErr)
			}
		}},
		// TestMergeSiblingsSucceeds covers the simplest working case: two true
		// siblings selected with V then merged with m (their lowest common ancestor
		// is the parent they're already under, so this is a same-parent merge).
		{"MergeSiblingsSucceeds", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil, nil, t.TempDir())
			// rows: 0=2024, 1=June, 2=03, 3=09 — select rows 2 and 3
			m.visualMode = true
			m.visualAnchor = 2
			m.cursor = 3

			m.mergeSelection()

			if m.statusIsErr {
				t.Fatalf("expected success, got error status: %q", m.statusMsg)
			}
			r03 := nodeByID(m.rows, pid("2024/June/03"))
			// both are plain unrenamed Date folders, so the merge proposes the day
			// range they span ("03_09") — written straight onto the node, not as a
			// pending rename: it's the merge's own output, not something the
			// reviewer typed, and [u] must revert it cleanly (see MergeNodes'
			// dayCombined handling in edit.go)
			if r03 == nil || r03.node.Name != "03_09" {
				t.Fatalf("want the surviving node named %q, got %+v", "03_09", r03)
			}
			if r09 := nodeByID(m.rows, pid("2024/June/09")); r09 != nil {
				t.Error("09 should be folded into 03, not still its own row")
			}
			if got := r03.node.MergedIDs; len(got) != 1 || got[0] != pid("2024/June/09") {
				t.Errorf("MergedIDs = %v, want [2024/June/09] so Confirm remaps its files too", got)
			}
			if r03.node.FileCount != 2 {
				t.Errorf("merged node FileCount = %d, want 2 (both leaves' files)", r03.node.FileCount)
			}
			if m.visualMode {
				t.Error("visualMode should be cleared after a merge")
			}
			if len(m.edits) != 1 {
				t.Errorf("undo stack = %d deep, want 1 step recorded", len(m.edits))
			}
		}},
		// TestUndoAfterDayRangeMergeLeavesNoStrayRename covers the reported bug:
		// merging Date folders wrote the combined range through row.newName (an
		// ID-keyed pending-rename map), and MergeNodes keeps the survivor's
		// original ID — so after [u] reverted the tree, the now-separate-again
		// "03" row inherited "03_09" as a leftover pending rename it never asked
		// for, rendering as a confusing "03 → 03_09" even though the merge had
		// just been undone. The combine must be baked into the tree snapshot
		// itself (Node.Name), not left in a side map undo doesn't know to clear.
		{"UndoAfterDayRangeMergeLeavesNoStrayRename", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.visualAnchor, m.cursor, m.visualMode = 2, 3, true
			m.mergeSelection()
			if m.statusIsErr {
				t.Fatalf("merge failed: %q", m.statusMsg)
			}

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
			rm := next.(Model)

			r03 := nodeByID(rm.rows, pid("2024/June/03"))
			if r03 == nil || r03.node.Name != "03" {
				t.Errorf("after undo want 03 back on its own under its proposed name, got %+v", r03)
			}
			if r09 := nodeByID(rm.rows, pid("2024/June/09")); r09 == nil || r09.node.Name != "09" {
				t.Errorf("after undo want 09 back as its own row under its proposed name, got %+v", r09)
			}
		}},
		// TestMergeSurvivorIsTheAnchorNotTheTopmostRow covers the reported bug:
		// V pressed on the lower row, then the cursor moved UP to extend the
		// selection. selectedRows() normalizes lo/hi to tree order for iteration,
		// but the anchor — not whichever row ends up topmost — must still name
		// the merged folder. Uses non-date sibling names ("Goa"/"Mumbai") rather
		// than siblingTree's "03"/"09": the day-range combine is order-independent
		// (min/max), so it can't tell "anchor" apart from "topmost" — this needs a
		// case the combine doesn't touch.
		{"MergeSurvivorIsTheAnchorNotTheTopmostRow", func(t *testing.T) {
			tree := []vfs.Node{{ID: pid("2024"), Name: "2024", Children: []vfs.Node{
				{ID: pid("2024/June"), Name: "June", Children: []vfs.Node{
					{ID: pid("2024/June/Goa"), Name: "Goa", FileCount: 1},
					{ID: pid("2024/June/Mumbai"), Name: "Mumbai", FileCount: 1},
				}},
			}}}
			m := newModel(tree, nil, nil, nil, nil, nil, t.TempDir())
			// rows: 0=2024, 1=June, 2=Goa, 3=Mumbai — press V on Mumbai, extend UP to Goa
			m.visualAnchor = 3
			m.cursor = 2
			m.visualMode = true

			m.mergeSelection()

			if m.statusIsErr {
				t.Fatalf("expected success, got error status: %q", m.statusMsg)
			}
			if rGoa := nodeByID(m.rows, pid("2024/June/Goa")); rGoa != nil {
				t.Error("Goa should be folded into Mumbai (the anchor), not still its own row")
			}
			rMumbai := nodeByID(m.rows, pid("2024/June/Mumbai"))
			if rMumbai == nil || rMumbai.node.Name != "Mumbai" {
				t.Fatalf("want the anchor row still named %q, got %+v", "Mumbai", rMumbai)
			}
			if got := rMumbai.node.MergedIDs; len(got) != 1 || got[0] != pid("2024/June/Goa") {
				t.Errorf("MergedIDs = %v, want [2024/June/Goa]", got)
			}
		}},
		// TestMergeAcrossBranchesCollapsesToOneNode covers the real reported case:
		// merging the same camera's leaves out of three different Month/Day branches
		// must leave exactly ONE folder under the Year holding all the files — not
		// three same-named siblings next to three now-empty Month/Day chains, which
		// is what the reviewer sees as "merge didn't work" even though Confirm would
		// have collapsed the paths later.
		{"MergeAcrossBranchesCollapsesToOneNode", func(t *testing.T) {
			m := newModel(crossBranchTree(), nil, nil, nil, nil, nil, t.TempDir())
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

			year := vfs.FindNode(m.tree, pid("2017"))
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
				if n := vfs.FindNode(m.tree, pid("2017/"+month)); n != nil {
					t.Errorf("%s should have been pruned — nothing left under it", month)
				}
			}
		}},
		// TestMergeAcrossBranchesRejectsWithNoCommonAncestor covers selecting leaves
		// that share literally no ancestor (different years) — nothing sensible to
		// reparent under.
		{"MergeAcrossBranchesRejectsWithNoCommonAncestor", func(t *testing.T) {
			tree := []vfs.Node{
				{ID: pid("2017"), Name: "2017", Children: []vfs.Node{{ID: pid("2017/Camera"), Name: "Camera", FileCount: 1}}},
				{ID: pid("2018"), Name: "2018", Children: []vfs.Node{{ID: pid("2018/Camera"), Name: "Camera", FileCount: 1}}},
			}
			m := newModel(tree, nil, nil, nil, nil, nil, t.TempDir())
			// rows: 0=2017, 1=2017/Camera, 2=2018, 3=2018/Camera
			m.visualAnchor = 1
			m.cursor = 3
			m.visualMode = true

			m.mergeSelection()

			if !m.statusIsErr {
				t.Errorf("expected rejection for leaves with no common ancestor, got success: %q", m.statusMsg)
			}
		}},
		// TestFlattenCollapsesEverythingBelowTheCursor covers [D]: the folder under
		// the cursor absorbs its whole subtree, so all its files sit directly in it.
		// The folder itself stays — only what's below it goes.
		{"FlattenCollapsesEverythingBelowTheCursor", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil, nil, t.TempDir())
			// rows: 0=2023, 1=April, 2=Indore, 3=iPhone, 4=August, 5=Indore, 6=iPhone
			m.cursor = 1 // April
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
			rm := next.(Model)
			if rm.statusIsErr {
				t.Fatalf("flatten April: %q", rm.statusMsg)
			}

			april := vfs.FindNode(rm.tree, pid("2023/April"))
			if april == nil || len(april.Children) != 0 {
				t.Fatalf("April = %+v, want a childless node", april)
			}
			if april.FileCount != 10 {
				t.Errorf("April FileCount = %d, want 10 (unchanged — it already counted the subtree)", april.FileCount)
			}
			// both dropped levels must remap, or the files in the deepest one keep
			// their old target_path when Confirm runs
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
			// August is untouched — [D] acts on the cursor's subtree, nothing else
			if aug := vfs.FindNode(rm.tree, pid("2023/August/Indore/Apple iPhone 13")); aug == nil {
				t.Error("August's subtree should be untouched by a flatten on April")
			}
		}},
		// TestFlattenWorksOnATopLevelRow covers the difference from [d]: flattening a
		// Year keeps the Year itself, so the files have somewhere to go.
		{"FlattenWorksOnATopLevelRow", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.cursor = 0 // 2023

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
			rm := next.(Model)

			if rm.statusIsErr {
				t.Fatalf("expected success, got %q", rm.statusMsg)
			}
			if len(rm.rows) != 1 || rm.rows[0].node.ID != pid("2023") || rm.rows[0].node.FileCount != 13 {
				t.Fatalf("rows = %+v, want just 2023 holding all 13 files", rm.rows)
			}
			if len(rm.tree[0].MergedIDs) != 6 {
				t.Errorf("MergedIDs = %v, want all six descendants", rm.tree[0].MergedIDs)
			}
		}},
		// TestFlattenLeafIsRejected covers the no-op guard.
		{"FlattenLeafIsRejected", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.cursor = 3 // the deepest row, nothing below it

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
			rm := next.(Model)

			if !rm.statusIsErr {
				t.Errorf("expected a rejection flattening a leaf, got %q", rm.statusMsg)
			}
			if len(rm.rows) != 7 {
				t.Errorf("tree changed on a rejected flatten: %d rows, want 7", len(rm.rows))
			}
		}},
		// TestDropSingleFolderLiftsItsChildren covers [d]: one folder, its children
		// reattached to its parent rather than deleted along with it.
		{"DropSingleFolderLiftsItsChildren", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.cursor = 2 // April's Indore, which still has the device child
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			rm := next.(Model)

			if rm.statusIsErr {
				t.Fatalf("expected success, got %q", rm.statusMsg)
			}
			april := vfs.FindNode(rm.tree, pid("2023/April"))
			if len(april.Children) != 1 || april.Children[0].Name != "Apple iPhone 13" {
				t.Fatalf("April children = %+v, want the lifted device node", april.Children)
			}
			if vfs.FindNode(rm.tree, pid("2023/August/Indore")) == nil {
				t.Error("[d] dropped more than the cursor's folder — August's Indore should be untouched")
			}
			if got := april.MergedIDs; len(got) != 1 || got[0] != pid("2023/April/Indore") {
				t.Errorf("MergedIDs = %v, want just the dropped folder", got)
			}
		}},
		// TestDropTopLevelIsRejected covers the guard: a Year has no parent to lift
		// files into, so dropping it would dump them in the library root.
		{"DropTopLevelIsRejected", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.cursor = 0

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			rm := next.(Model)

			if !rm.statusIsErr {
				t.Errorf("expected rejection dropping a top-level folder, got %q", rm.statusMsg)
			}
			if len(rm.rows) != 7 {
				t.Errorf("tree changed on a rejected drop: %d rows, want 7", len(rm.rows))
			}
		}},
		// TestUndoRestoresTreeAfterFlatten covers [u] on a flatten — same whole-tree
		// snapshot the merge undo uses.
		{"UndoRestoresTreeAfterFlatten", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.cursor = 1
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
			rm := next.(Model)

			next2, _ := rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
			rm2 := next2.(Model)

			if vfs.FindNode(rm2.tree, pid("2023/April/Indore/Apple iPhone 13")) == nil {
				t.Error("April's subtree should be back after undo")
			}
			if april := vfs.FindNode(rm2.tree, pid("2023/April")); len(april.MergedIDs) != 0 {
				t.Errorf("undo left MergedIDs behind: %v", april.MergedIDs)
			}
		}},
		// TestUndoRestoresTreeAfterCrossBranchMerge covers [u] on the new structural
		// merge: it must restore the whole pre-merge tree (the old per-row newName
		// undo can't undo a reparent), and stops cleanly once the stack is empty.
		{"UndoRestoresTreeAfterCrossBranchMerge", func(t *testing.T) {
			m := newModel(crossBranchTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.visualAnchor = 3
			m.cursor = 9
			m.visualMode = true
			m.mergeSelection()
			if m.statusIsErr {
				t.Fatalf("merge failed: %q", m.statusMsg)
			}

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
			rm := next.(Model)

			april20 := vfs.FindNode(rm.tree, pid("2017/April/20"))
			if april20 == nil || len(april20.Children) != 1 || april20.Children[0].Name != "Canon EOS 700D" {
				t.Errorf("2017/April/20 should have its Canon EOS 700D leaf back after undo, got %+v", april20)
			}
			year := vfs.FindNode(rm.tree, pid("2017"))
			for _, c := range year.Children {
				if c.Name == "Canon EOS 700D" {
					t.Error("2017 should not have a direct Canon EOS 700D child after undo")
				}
			}
			if len(rm.edits) != 0 {
				t.Error("undo stack should be empty again after undoing the only edit")
			}

			// pressing u again with nothing pending must be a no-op, not a crash
			next2, _ := rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
			rm2 := next2.(Model)
			if len(rm2.edits) != 0 {
				t.Error("second undo should still be a no-op")
			}
		}},
		// TestVisibleRowsShrinksWhenHelpWraps covers the reported overflow: on a
		// narrow terminal the key help wraps to several lines, and the tree budget has
		// to shrink by exactly that much or the bottom rows run off the screen.
		{"VisibleRowsShrinksWhenHelpWraps", func(t *testing.T) {
			wide := newModel(sampleTree(), nil, nil, nil, nil, nil, t.TempDir())
			wide.height, wide.width = 24, 200
			narrow := wide
			narrow.width = 40

			if wide.visibleRows() <= narrow.visibleRows() {
				t.Errorf("visibleRows: wide %d, narrow %d — narrow must be smaller (help wraps into the tree's space)",
					wide.visibleRows(), narrow.visibleRows())
			}
			// the whole frame must fit the terminal, however the help wrapped
			for _, m := range []Model{wide, narrow} {
				total := lipgloss.Height(m.header()) + m.visibleRows() + lipgloss.Height(m.footer())
				if total > m.height {
					t.Errorf("width %d: frame is %d lines, want <= %d", m.width, total, m.height)
				}
			}
		}},
		// TestQuitWithNoEditsExitsImmediately covers the other side: nothing typed,
		// nothing to lose, no nagging.
		{"QuitWithNoEditsExitsImmediately", func(t *testing.T) {
			m := newModel(sampleTree(), nil, nil, nil, nil, nil, t.TempDir())
			if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
				t.Error("ctrl+c with a clean tree should quit straight away")
			}
		}},
		// TestStructuralEditKeepsEarlierRenames covers a silent data-loss bug: merge,
		// delete and undo all rebuild the row list from the tree. A rename on a row
		// the edit never touched has to survive that.
		// Year and month folders are fixed (D26): [r] on one is refused with a
		// status line, and the tree is left as it was.
		{"RenameRefusedOnYearAndMonth", func(t *testing.T) {
			tree := siblingTree()
			tree[0].Level, tree[0].Children[0].Level = vfs.LevelYear, vfs.LevelMonth
			for _, row := range []int{0, 1} {
				m := newModel(vfs.CloneTree(tree), nil, nil, nil, nil, nil, t.TempDir())
				m.cursor = row
				next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
				rm := next.(Model)
				if rm.editing || !rm.statusIsErr {
					t.Errorf("row %d: editing = %v, statusIsErr = %v, want a refusal", row, rm.editing, rm.statusIsErr)
				}
				rm.applyRename("Renamed")
				if got := rm.rows[row].node.Name; got == "Renamed" {
					t.Errorf("row %d renamed to %q, want it unchanged", row, got)
				}
			}
		}},
		{"StructuralEditKeepsEarlierRenames", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil, nil, t.TempDir())
			// rows: 0=2024, 1=June, 2=03, 3=09. Rename the year, then merge the leaves.
			m.cursor = 0
			m.applyRename("Two Thousand Twenty Four")

			m.visualMode, m.visualAnchor, m.cursor = true, 2, 3
			m.mergeSelection()

			if m.statusIsErr {
				t.Fatalf("merge failed: %q", m.statusMsg)
			}
			if got := m.rows[0].node.Name; got != "Two Thousand Twenty Four" {
				t.Errorf("after merge, year name = %q, want the rename to survive", got)
			}
		}},
		// TestUndoKeepsEarlierRenames is the undo half: [u] walks back one edit, not
		// every edit — the rename made before the merge stays.
		{"UndoKeepsEarlierRenames", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.cursor = 0
			m.applyRename("Renamed Year")
			m.visualMode, m.visualAnchor, m.cursor = true, 2, 3
			m.mergeSelection()

			next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
			rm := next.(Model)
			if got := rm.rows[0].node.Name; got != "Renamed Year" {
				t.Errorf("after undo, year name = %q, want %q", got, "Renamed Year")
			}
			if nodeByID(rm.rows, pid("2024/June/09")) == nil {
				t.Error("undo should have restored the folded-away leaf")
			}
		}},
		// TestRefreshSuggestionsFiltersInMemory covers that typing never touches the
		// DB or the resolver: both sources are pre-loaded, and each keystroke only
		// narrows what's already in memory. A nil db/resolver here would panic or
		// return nothing if refreshSuggestions still queried.
		{"RefreshSuggestionsFiltersInMemory", func(t *testing.T) {
			m := newModel(sampleTree(), nil, context.Background(), nil, nil, nil, t.TempDir())
			m.geoCands = []location.Candidate{
				{Name: "Manali", DistKM: 3},
				{Name: "Mandi", DistKM: 40},
				{Name: "Kullu", DistKM: 12},
			}
			m.labels = []string{"Manali Trip", "Goa 2024"}

			m.input = ""
			m.refreshSuggestions()
			if len(m.suggestions) != 3 {
				t.Fatalf("no prefix: got %d suggestions, want all 3 geo candidates", len(m.suggestions))
			}

			m.input = "man"
			m.refreshSuggestions()
			var names []string
			for _, s := range m.suggestions {
				names = append(names, s.Label)
			}
			if len(names) != 3 || names[0] != "Manali" || names[1] != "Mandi" || names[2] != "Manali Trip" {
				t.Errorf("suggestions = %v, want [Manali Mandi Manali Trip] (geo first, then labels)", names)
			}

			m.input = "zzz"
			m.refreshSuggestions()
			if len(m.suggestions) != 0 {
				t.Errorf("suggestions = %+v, want none for a non-matching prefix", m.suggestions)
			}
		}},
		// TestRenameLoadsGeoCandidatesOnce covers the split: [r] and ctrl+e are the
		// only keys that hit the resolver; plain typing must not. With a nil resolver
		// loadGeoCandidates is a no-op, so this asserts the wiring via the keystroke
		// path not panicking and suggestions still filtering from pre-loaded labels.
		{"RenameLoadsGeoCandidatesOnce", func(t *testing.T) {
			m := newModel(sampleTree(), nil, context.Background(), nil, nil, nil, t.TempDir())
			m.labels = []string{"Manali"}
			m.cursor = 1 // the June leaf

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
			rm := next.(Model)
			if !rm.editing {
				t.Fatal("expected [r] to open the rename editor")
			}
			if rm.radiusDelta != location.NearSearchDegrees {
				t.Errorf("radiusDelta = %v, want it seeded to %v", rm.radiusDelta, location.NearSearchDegrees)
			}

			// [r] pre-fills the input with the current name ("June") — clear it the way
			// a reviewer would, then type. Every one of these keystrokes must resolve
			// against pre-loaded data only; the nil db/resolver here proves it.
			for range len(rm.input) {
				next, _ = rm.Update(tea.KeyMsg{Type: tea.KeyBackspace})
				rm = next.(Model)
			}
			for _, r := range []rune("Man") {
				next, _ = rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				rm = next.(Model)
			}
			if len(rm.suggestions) != 1 || rm.suggestions[0].Label != "Manali" {
				t.Errorf("suggestions = %+v, want the pre-loaded label filtered in memory", rm.suggestions)
			}
		}},
		// TestRenameArrowsPickASuggestion covers the reported bug: ↑/↓ and tab did
		// nothing in the rename editor, so a listed place could only be retyped by
		// hand. They now walk the list like the config wizard's completions —
		// enter on an arrowed-onto row picks it, the next enter applies.
		{"RenameArrowsPickASuggestion", func(t *testing.T) {
			m := newModel(sampleTree(), nil, context.Background(), nil, nil, nil, t.TempDir())
			m.labels = []string{"Manali", "Mandi"}
			m.cursor = 1 // the June leaf
			m.input, m.editing = "Man", true
			m.refreshSuggestions()
			if m.suggCursor != -1 {
				t.Fatalf("suggCursor = %d, want -1 before any arrow key", m.suggCursor)
			}

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
			next, _ = next.(Model).Update(tea.KeyMsg{Type: tea.KeyDown})
			rm := next.(Model)
			if rm.suggCursor != 1 {
				t.Fatalf("suggCursor = %d after two ↓, want 1 (the second suggestion)", rm.suggCursor)
			}

			next, _ = rm.Update(tea.KeyMsg{Type: tea.KeyEnter}) // pick, don't apply
			rm = next.(Model)
			if !rm.editing || rm.input != "Mandi" {
				t.Fatalf("after enter on a picked row: editing=%v input=%q, want still editing with %q",
					rm.editing, rm.input, "Mandi")
			}

			next, _ = rm.Update(tea.KeyMsg{Type: tea.KeyEnter}) // now apply
			rm = next.(Model)
			if rm.editing {
				t.Error("the second enter should close the editor")
			}
			if got := rm.rows[rm.cursor].node.Name; got != "Mandi" {
				t.Errorf("node name = %q, want the rename written straight onto the node", got)
			}
		}},
		// TestRenameUndoRestoresTheOldName covers the other half of the same report:
		// a rename used to sit in a side field rendered as "old → new", which [u]
		// did not clear. The rename is a tree edit like any other now.
		{"RenameUndoRestoresTheOldName", func(t *testing.T) {
			m := newModel(siblingTree(), nil, context.Background(), nil, nil, nil, t.TempDir())
			m.cursor = 2 // the "03" day
			m.applyRename("Goa")
			if got := m.rows[m.cursor].node.Name; got != "Goa" {
				t.Fatalf("node name = %q, want %q", got, "Goa")
			}

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
			rm := next.(Model)
			if row := nodeByID(rm.rows, pid("2024/June/03")); row == nil || row.node.Name != "03" {
				t.Fatalf("after undo want the day back as %q, got %+v", "03", row)
			}
			if len(rm.edits) > 0 {
				t.Error("undoing the only edit should leave nothing pending")
			}
		}},
		// TestMergingParentsCollapsesSameNamedChildren covers the reported case:
		// merging three Day folders of one trip must leave a single Goa underneath,
		// not three the reviewer then has to merge by hand. Children that genuinely
		// differ (a second camera) stay separate.
		{"MergingParentsCollapsesSameNamedChildren", func(t *testing.T) {
			m := newModel(tripTree(), nil, nil, nil, nil, nil, t.TempDir())
			// rows: 0=2024 1=06_June 2=03 3=Goa 4=iPhone 5=04 6=Goa 7=iPhone 8=05 9=Goa 10=Canon
			m.visualAnchor, m.cursor, m.visualMode = 2, 10, true

			m.mergeSelection()

			if m.statusIsErr {
				t.Fatalf("expected success, got %q", m.statusMsg)
			}
			june := vfs.FindNode(m.tree, pid("2024/06_June"))
			if len(june.Children) != 1 {
				t.Fatalf("June has %d children, want 1 merged day", len(june.Children))
			}
			day := june.Children[0]
			if day.FileCount != 3 {
				t.Errorf("merged day FileCount = %d, want 3", day.FileCount)
			}
			if len(day.Children) != 1 || day.Children[0].Name != "Goa" {
				t.Fatalf("merged day children = %+v, want exactly one Goa", day.Children)
			}
			goa := day.Children[0]
			if goa.FileCount != 3 || len(goa.MergedIDs) != 2 {
				t.Errorf("Goa: files=%d mergedIDs=%v, want 3 files and the two folded-away Goa ids", goa.FileCount, goa.MergedIDs)
			}
			// the two iPhones collapse; the Canon is a genuinely different folder
			names := map[string]int{}
			for _, c := range goa.Children {
				names[c.Name] = c.FileCount
			}
			if len(goa.Children) != 2 || names["iPhone"] != 2 || names["Canon"] != 1 {
				t.Errorf("Goa children = %+v, want iPhone(2) and Canon(1)", goa.Children)
			}
		}},
		// TestMergeUsesAnchorDepth covers the selection rule: rows deeper than the row
		// [V] was pressed on are that folder's contents and ride along, they are not
		// merge candidates of their own.
		{"MergeUsesAnchorDepth", func(t *testing.T) {
			m := newModel(tripTree(), nil, nil, nil, nil, nil, t.TempDir())
			// anchor on 03's Goa (depth 3) through 05's Goa — merges the Goas, not the days
			m.visualAnchor, m.cursor, m.visualMode = 3, 9, true

			m.mergeSelection()

			if m.statusIsErr {
				t.Fatalf("expected success, got %q", m.statusMsg)
			}
			// all three Goas merged under their lowest common ancestor, the month
			june := vfs.FindNode(m.tree, pid("2024/06_June"))
			if len(june.Children) != 1 || june.Children[0].Name != "Goa" {
				t.Fatalf("June children = %+v, want one Goa (the days were emptied and pruned)", june.Children)
			}
		}},
		// TestMergeRespectsRenames covers name matching: children collapse on the
		// name they will actually be written as, not the one they started with.
		{"MergeRespectsRenames", func(t *testing.T) {
			m := newModel(tripTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.cursor = 10
			m.applyRename("iPhone") // rename 05's Canon to match the others
			m.visualAnchor, m.cursor, m.visualMode = 2, 10, true

			m.mergeSelection()

			goa := vfs.FindNode(m.tree, pid("2024/06_June/03/Goa"))
			if goa == nil || len(goa.Children) != 1 {
				t.Fatalf("Goa children = %+v, want one — the renamed Canon collapses into iPhone", goa)
			}
		}},
		// TestMergeKeepsTheAnchorsOwnName covers the naming rule with non-date
		// names, where the day-range combine (a different feature — see
		// TestMergeNodesCombinesDayRanges) can't mask what the merge chose: the
		// survivor is named after the row [V] was pressed on, nothing else.
		{"MergeKeepsTheAnchorsOwnName", func(t *testing.T) {
			tree := []vfs.Node{{ID: pid("2017"), Name: "2017", FileCount: 2, Children: []vfs.Node{
				{ID: pid("2017/Mumbai"), Name: "Mumbai", FileCount: 1},
				{ID: pid("2017/Goa"), Name: "Goa", FileCount: 1},
			}}}
			m := newModel(tree, nil, nil, nil, nil, nil, t.TempDir())
			m.visualAnchor, m.cursor, m.visualMode = 1, 2, true

			m.mergeSelection()

			if m.statusIsErr {
				t.Fatalf("expected success, got %q", m.statusMsg)
			}
			row := nodeByID(m.rows, pid("2017/Mumbai"))
			if row == nil {
				t.Fatal("expected the first pick to survive the merge")
			}
			if row.node.Name != "Mumbai" {
				t.Errorf("merged folder name = %q, want the first pick's own name %q", row.node.Name, "Mumbai")
			}
		}},
		// TestMergeKeepsAnExplicitRename is the other half: a rename the reviewer
		// typed on the first pick *is* what the merged folder should be called.
		{"MergeKeepsAnExplicitRename", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.cursor = 2
			m.applyRename("Goa Trip") // the cursor follows the node through the re-sort
			m.visualAnchor, m.visualMode = m.cursor, true
			m.cursor = 2 // the other day

			m.mergeSelection()

			row := nodeByID(m.rows, pid("2024/June/03"))
			if row == nil || row.node.Name != "Goa Trip" {
				t.Fatalf("want the typed rename carried onto the merged folder, got %+v", row)
			}
		}},
		// TestUndoWalksBackThroughEveryEdit covers multi-level undo: several
		// structural edits in a row must each be reversible, in order, all the way to
		// the tree the review started with.
		{"UndoWalksBackThroughEveryEdit", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil, nil, t.TempDir())
			before := len(m.rows)

			// three edits: drop a folder, flatten a subtree, drop another folder
			m.cursor = 2 // April's Indore
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			rm := next.(Model)
			rm.cursor = 1 // April
			next, _ = rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
			rm = next.(Model)
			rm.cursor = 3 // August's Indore
			next, _ = rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			rm = next.(Model)

			if len(rm.edits) != 3 {
				t.Fatalf("undo stack = %d deep, want 3", len(rm.edits))
			}
			for i := 3; i > 0; i-- {
				next, _ = rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
				rm = next.(Model)
				if len(rm.edits) != i-1 {
					t.Fatalf("after undo %d: stack = %d deep, want %d", 4-i, len(rm.edits), i-1)
				}
				if rm.statusIsErr {
					t.Fatalf("undo %d reported an error: %q", 4-i, rm.statusMsg)
				}
			}

			if len(rm.rows) != before {
				t.Errorf("%d rows after undoing everything, want the original %d", len(rm.rows), before)
			}
			if vfs.FindNode(rm.tree, pid("2023/April/Indore/Apple iPhone 13")) == nil {
				t.Error("the original tree should be fully restored")
			}

			// one more undo is a flagged no-op, not a crash
			next, _ = rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
			if rm2 := next.(Model); !rm2.statusIsErr {
				t.Errorf("undo on an empty stack should say so, got %q", rm2.statusMsg)
			}
		}},
		// TestVisualFlattenActsOnEverySelectedFolder covers [V] + [D]: several
		// locations under one Day each lose their splits and keep their own folder.
		// They are not merged — that is [m]'s job.
		{"VisualFlattenActsOnEverySelectedFolder", func(t *testing.T) {
			m := newModel(dayWithLocationsTree(), nil, nil, nil, nil, nil, t.TempDir())
			// rows: 0=2024 1=06_June 2=03 3=Goa 4=iPhone 5=Vertical 6=Panaji ... 9=Margao ...
			m.visualAnchor, m.cursor, m.visualMode = 3, 11, true

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
			rm := next.(Model)
			if rm.statusIsErr {
				t.Fatalf("expected success, got %q", rm.statusMsg)
			}

			day := vfs.FindNode(rm.tree, pid("2024/06_June/03"))
			if len(day.Children) != 3 {
				t.Fatalf("day has %d children, want the 3 locations still separate", len(day.Children))
			}
			for _, c := range day.Children {
				if len(c.Children) != 0 {
					t.Errorf("%s still has %d subfolders, want them flattened in", c.Name, len(c.Children))
				}
				if len(c.MergedIDs) != 2 {
					t.Errorf("%s MergedIDs = %v, want its device and orientation folders", c.Name, c.MergedIDs)
				}
			}
			if rm.visualMode {
				t.Error("visual selection should be cleared after the flatten")
			}
			if len(rm.edits) != 1 {
				t.Errorf("undo stack = %d, want one step for the whole multi-folder flatten", len(rm.edits))
			}
		}},
		// TestVisualDropActsOnEverySelectedFolder covers [V] + [d]: each selected
		// folder goes and its children are lifted onto the parent they shared.
		{"VisualDropActsOnEverySelectedFolder", func(t *testing.T) {
			m := newModel(dayWithLocationsTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.visualAnchor, m.cursor, m.visualMode = 3, 11, true

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			rm := next.(Model)
			if rm.statusIsErr {
				t.Fatalf("expected success, got %q", rm.statusMsg)
			}

			day := vfs.FindNode(rm.tree, pid("2024/06_June/03"))
			if len(day.Children) != 3 {
				t.Fatalf("day children = %d, want the three lifted iPhone folders", len(day.Children))
			}
			for _, c := range day.Children {
				if c.Name != "iPhone" {
					t.Errorf("day child = %q, want the lifted iPhone folder", c.Name)
				}
			}
			if len(day.MergedIDs) != 3 {
				t.Errorf("day MergedIDs = %v, want the three dropped locations", day.MergedIDs)
			}
		}},
		// TestVisualFlattenIgnoresDeeperRowsInTheRange covers the anchor-depth rule
		// for [D]: the splits inside a selected location are its contents, not
		// separate flatten targets, so the Day above and the rows below are untouched.
		{"VisualFlattenIgnoresDeeperRowsInTheRange", func(t *testing.T) {
			m := newModel(dayWithLocationsTree(), nil, nil, nil, nil, nil, t.TempDir())
			// anchor on Goa (depth 3), extend only into its own subtree
			m.visualAnchor, m.cursor, m.visualMode = 3, 5, true

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
			rm := next.(Model)

			if goa := vfs.FindNode(rm.tree, pid("2024/06_June/03/Goa")); len(goa.Children) != 0 {
				t.Error("Goa should be flattened")
			}
			if p := vfs.FindNode(rm.tree, pid("2024/06_June/03/Panaji")); p == nil || len(p.Children) != 1 {
				t.Error("Panaji was outside the range and must be untouched")
			}
		}},
		// TestJumpSameDepthCrossesBranches covers [n]/[N]: the cursor hops to the next
		// row at its own indent depth wherever that is, so a deep tree is walkable
		// without scrolling through every folder's contents — and [V] plus [n] can
		// select one level across several branches.
		{"JumpSameDepthCrossesBranches", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil, nil, t.TempDir())
			// rows: 0=2023 1=April 2=Indore 3=iPhone 4=August 5=Indore 6=iPhone
			m.cursor = 2 // April's Indore, depth 2

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
			rm := next.(Model)
			if rm.cursor != 5 {
				t.Fatalf("cursor = %d, want 5 (August's Indore — the next row at depth 2)", rm.cursor)
			}
			if rm.statusIsErr {
				t.Errorf("unexpected error status: %q", rm.statusMsg)
			}

			// N goes back
			back, _ := rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
			if got := back.(Model).cursor; got != 2 {
				t.Errorf("cursor = %d after N, want 2", got)
			}
		}},
		// TestJumpSameDepthStopsAtTheEnds covers the no-wrap guard: past the last row
		// at this level it says so instead of silently looping to the top.
		{"JumpSameDepthStopsAtTheEnds", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.cursor = 5 // the last depth-2 row

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
			rm := next.(Model)

			if rm.cursor != 5 {
				t.Errorf("cursor moved to %d, want to stay at 5", rm.cursor)
			}
			if !rm.statusIsErr {
				t.Errorf("expected a flagged 'no more folders at this level', got %q", rm.statusMsg)
			}
		}},
		// TestJumpSameDepthExtendsAVisualSelection covers the reason [n] earns its
		// key: V, then n, selects a whole level across branches without arrowing
		// through the folders in between.
		{"JumpSameDepthExtendsAVisualSelection", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.cursor = 2
			m.visualMode, m.visualAnchor = true, 2

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
			rm := next.(Model)

			sel := rm.selectedRows()
			if len(sel) != 2 {
				t.Fatalf("selected %d rows, want both Indore folders", len(sel))
			}
			for _, r := range sel {
				if r.node.Name != "Indore" {
					t.Errorf("selected %q, want only the depth-2 Indore rows", r.node.Name)
				}
			}
		}},
		// TestStructuralEditsKeepNameOrder covers the reported "the merge deleted my
		// folder": the merged node was appended to the end of its parent's children,
		// so a 575-file day jumped below its siblings and looked gone. Every level
		// stays in the same name order BuildTree emits.
		{"StructuralEditsKeepNameOrder", func(t *testing.T) {
			day := func(n string, files int, kids ...vfs.Node) vfs.Node {
				return vfs.Node{ID: pid("2017/12_December/" + n), Name: n, FileCount: files, Children: kids}
			}
			tree := []vfs.Node{{ID: pid("2017"), Name: "2017", FileCount: 40, Children: []vfs.Node{
				{ID: pid("2017/12_December"), Name: "12_December", FileCount: 40, Children: []vfs.Node{
					day("16", 4), day("20", 12), day("21", 20), day("22", 3), day("25", 1),
				}},
			}}}
			m := newModel(tree, nil, nil, nil, nil, nil, t.TempDir())
			// rows: 0=2017 1=12_December 2=16 3=20 4=21 5=22 6=25 — merge 21 and 22
			m.visualAnchor, m.cursor, m.visualMode = 4, 5, true
			m.mergeSelection()

			dec := vfs.FindNode(m.tree, pid("2017/12_December"))
			var names []string
			for _, c := range dec.Children {
				names = append(names, c.Name)
			}
			if !slices.IsSorted(names) {
				t.Errorf("December children = %v, want name order — an appended merge result reads as a deleted folder", names)
			}
			// 21 and 22 are plain Date folders, so the merge names the result the
			// day range they span ("21_22") — see TestMergeNodesCombinesDayRanges
			if len(names) != 4 || names[2] != "21_22" {
				t.Errorf("children = %v, want the merged 21_22 back in its sorted position", names)
			}
			// and the cursor follows the merged folder rather than staying on an index
			if m.rows[m.cursor].node.ID != pid("2017/12_December/21") {
				t.Errorf("cursor is on %d, want the merged folder", m.rows[m.cursor].node.ID)
			}
		}},
		// TestDropKeepsNameOrder covers the same for lifted children.
		{"DropKeepsNameOrder", func(t *testing.T) {
			m := newModel(dayWithLocationsTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.visualAnchor, m.cursor, m.visualMode = 3, 11, true
			m.dropFolders(m.selectedRows())

			day := vfs.FindNode(m.tree, pid("2024/06_June/03"))
			var ids []int64
			for _, c := range day.Children {
				ids = append(ids, c.ID)
			}
			var names []string
			for _, c := range day.Children {
				names = append(names, c.Name)
			}
			if !slices.IsSorted(names) {
				t.Errorf("lifted children = %v (%v), want name order", names, ids)
			}
		}},
		// TestRowsRenderBoxDrawingGuides covers the guides tui.Guides fills in on
		// every row: a root gets no prefix, a lone child is a last child, and a
		// deeper row's prefix carries a blank continuation under a last-child
		// ancestor rather than a bar.
		{"RowsRenderBoxDrawingGuides", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil, nil, t.TempDir())
			want := map[string]string{
				"2024":         "",
				"2024/June":    "└─ ",
				"2024/June/03": "   ├─ ",
				"2024/June/09": "   └─ ",
			}
			for id, wantGuide := range want {
				row := nodeByID(m.rows, pid(id))
				if row == nil {
					t.Fatalf("no row for %q", id)
				}
				if row.guide != wantGuide {
					t.Errorf("guide for %q = %q, want %q", id, row.guide, wantGuide)
				}
			}
		}},
		// TestViewRendersHeaderRowsAndFooter is a smoke test over the whole render
		// path — header, rows, footer — since none of it had any coverage.
		{"ViewRendersHeaderRowsAndFooter", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.height, m.width = 40, 80
			out := ansi.Strip(m.View())
			for _, want := range []string{"2024", "June", "03", "09", "4 folders", "1 files"} {
				if !strings.Contains(out, want) {
					t.Errorf("View() missing %q:\n%s", want, out)
				}
			}
		}},
		// TestViewShowsHelpWhenToggled covers the [?] full-screen key reference.
		{"ViewShowsHelpWhenToggled", func(t *testing.T) {
			m := newModel(sampleTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.height, m.width = 40, 80
			m.showHelp = true
			out := ansi.Strip(m.View())
			for _, want := range []string{"Moving", "Naming", "Reshaping", "Leaving", "merge the selected folders"} {
				if !strings.Contains(out, want) {
					t.Errorf("helpView missing %q:\n%s", want, out)
				}
			}
		}},
		// TestFooterShowsRenameSuggestions covers the editing branch of footer():
		// the rename prompt, the suggestion list with the picked row highlighted,
		// and the top-match tab hint.
		{"FooterShowsRenameSuggestions", func(t *testing.T) {
			m := newModel(sampleTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.height, m.width = 40, 80
			m.editing = true
			m.input = "Go"
			m.suggestions = []location.Suggestion{{Label: "Goa, India", Value: "Goa"}, {Label: "Gondia, India", Value: "Gondia"}}
			m.suggCursor = 1
			out := ansi.Strip(m.footer())
			for _, want := range []string{"Rename", "Goa, India", "Gondia, India", "pick", "wider", "search"} {
				if !strings.Contains(out, want) {
					t.Errorf("footer() missing %q:\n%s", want, out)
				}
			}
		}},
		// TestFooterShowsPreviewSpinnerAndError cover the previewing and
		// previewErr branches of footer(), neither of which had any coverage.
		{"FooterShowsPreviewSpinnerAndError", func(t *testing.T) {
			m := newModel(sampleTree(), nil, nil, nil, nil, nil, t.TempDir())
			m.height, m.width = 40, 80
			m.previewing = true
			if out := ansi.Strip(m.footer()); !strings.Contains(out, "Copying preview") {
				t.Errorf("footer() during preview missing spinner text:\n%s", out)
			}

			m.previewing = false
			m.previewErr = fmt.Errorf("disk full")
			if out := ansi.Strip(m.footer()); !strings.Contains(out, "Preview failed") || !strings.Contains(out, "disk full") {
				t.Errorf("footer() with previewErr missing failure text:\n%s", out)
			}
		}},
		// TestPeekCmdCopiesFilesToTheDeterministicDir drives peekCmd end to end
		// against a real DB: it should copy the node's files into the dir
		// previewDirFor names, leaving no .copying- staging dir behind.
		{"PeekCmdCopiesFilesToTheDeterministicDir", func(t *testing.T) {
			ctx := context.Background()
			usePreviewRoot(t)
			d := dbtest.New(t)
			// peekCmd copies real bytes from source_path, so the fixture's source
			// file has to actually exist on disk
			srcFile := filepath.Join(t.TempDir(), "a.jpg")
			if err := os.WriteFile(srcFile, []byte("hello"), 0o644); err != nil {
				t.Fatal(err)
			}
			insertVFSEntry(t, d, 1, srcFile, "2024/June/a.jpg")

			node := &vfs.Node{ID: nodeAt(t, d, "2024/June")}
			msg := peekCmd(ctx, d, node)()
			pm := msg.(previewDoneMsg)
			if pm.err != nil {
				t.Fatalf("peekCmd: %v", pm.err)
			}
			if want := previewDirFor([]string{srcFile}); pm.dir != want {
				t.Errorf("dir = %q, want the deterministic %q", pm.dir, want)
			}
			if _, err := os.Stat(filepath.Join(pm.dir, "a.jpg")); err != nil {
				t.Errorf("expected a.jpg copied into %s: %v", pm.dir, err)
			}
			entries, err := os.ReadDir(PreviewRoot())
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Errorf("preview root holds %d entries, want only the finished copy — a staging dir was left behind", len(entries))
			}
		}},
		// TestPeekCmdReusesAnEarlierSessionsCopy covers the whole point of the fixed
		// root: a completed copy left behind by an earlier run is handed back as-is,
		// with no second copy of the same bytes — and the reuse counts as a use, so
		// makeRoom's mtime order is least-recently-opened, not oldest-created.
		{"PeekCmdReusesAnEarlierSessionsCopy", func(t *testing.T) {
			ctx := context.Background()
			usePreviewRoot(t)
			d := dbtest.New(t)
			srcFile := filepath.Join(t.TempDir(), "a.jpg")
			if err := os.WriteFile(srcFile, []byte("hello"), 0o644); err != nil {
				t.Fatal(err)
			}
			insertVFSEntry(t, d, 1, srcFile, "2024/June/a.jpg")

			node := &vfs.Node{ID: nodeAt(t, d, "2024/June")}
			if pm := peekCmd(ctx, d, node)().(previewDoneMsg); pm.err != nil {
				t.Fatalf("first peek: %v", pm.err)
			}
			// a fresh copy would wipe the directory first, taking this with it
			dir := previewDirFor([]string{srcFile})
			witness := filepath.Join(dir, "witness")
			if err := os.WriteFile(witness, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			stale := time.Now().Add(-time.Hour)
			if err := os.Chtimes(dir, stale, stale); err != nil {
				t.Fatal(err)
			}

			pm := peekCmd(ctx, d, node)().(previewDoneMsg)
			if pm.err != nil {
				t.Fatalf("second peek: %v", pm.err)
			}
			if _, err := os.Stat(witness); err != nil {
				t.Errorf("second peek re-copied instead of reusing %s", pm.dir)
			}
			fi, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !fi.ModTime().After(stale) {
				t.Errorf("mtime = %v, want bumped past %v so the reuse counts as a use", fi.ModTime(), stale)
			}
		}},
		// TestPeekCmdLeavesNoDirWhenTheCopyFails covers what the atomic rename
		// buys: a copy that dies partway can never appear under the hash name, so
		// a later peek can't mistake three of forty files for a cache hit.
		{"PeekCmdLeavesNoDirWhenTheCopyFails", func(t *testing.T) {
			ctx := context.Background()
			usePreviewRoot(t)
			d := dbtest.New(t)
			// a source that isn't there fails copyFiles partway through the batch
			present := filepath.Join(t.TempDir(), "a.jpg")
			if err := os.WriteFile(present, []byte("hello"), 0o644); err != nil {
				t.Fatal(err)
			}
			missing := filepath.Join(t.TempDir(), "gone.jpg")
			insertVFSEntry(t, d, 1, present, "2024/June/a.jpg")
			insertVFSEntry(t, d, 2, missing, "2024/June/gone.jpg")

			pm := peekCmd(ctx, d, &vfs.Node{ID: nodeAt(t, d, "2024/June")})().(previewDoneMsg)
			if pm.err == nil {
				t.Fatal("expected an error copying a missing source file")
			}
			if _, err := os.Stat(previewDirFor([]string{present, missing})); !os.IsNotExist(err) {
				t.Error("a failed copy left a directory under the hash name, which a later peek would reuse")
			}
			entries, err := os.ReadDir(PreviewRoot())
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("preview root holds %d entries after a failed copy, want none", len(entries))
			}
		}},
		// TestMakeRoomEvictsLeastRecentlyUsedFirst covers the 5%-of-disk budget:
		// when a new preview doesn't fit, the copies opened longest ago go until it
		// does. peekCmd touches a copy it reuses, so mtime is the LRU order.
		{"MakeRoomEvictsLeastRecentlyUsedFirst", func(t *testing.T) {
			root := usePreviewRoot(t)
			var dirs []string
			for i, name := range []string{"coldest", "warmer", "warmest"} {
				dir := filepath.Join(root, name)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, 1024), 0o644); err != nil {
					t.Fatal(err)
				}
				stamp := time.Now().Add(time.Duration(i-3) * time.Hour)
				if err := os.Chtimes(dir, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				dirs = append(dirs, dir)
			}

			// free is stated, so the budget is exact: room for four copies, of
			// which the incoming one needs three — the two oldest must go.
			const free = 4*1024*previewBudgetDivisor - 3*1024
			if err := makeRoom(root, 3*1024, free); err != nil {
				t.Fatalf("makeRoom: %v", err)
			}

			for _, dir := range dirs[:2] {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Errorf("%s survived, want the least recently used copies evicted first", dir)
				}
			}
			if _, err := os.Stat(dirs[2]); err != nil {
				t.Errorf("most recently used copy evicted unnecessarily: %v", err)
			}
		}},
		// TestMakeRoomRefusesAnOversizedPreview covers the other end: no amount of
		// eviction makes a preview bigger than the whole budget fit.
		{"MakeRoomRefusesAnOversizedPreview", func(t *testing.T) {
			if err := makeRoom(usePreviewRoot(t), math.MaxInt64/2, 1024); err == nil {
				t.Error("expected an over-budget preview to be refused")
			}
		}},
		// TestPeekCmdReportsNoFilesUnderNode covers the empty-node error path.
		{"PeekCmdReportsNoFilesUnderNode", func(t *testing.T) {
			ctx := context.Background()
			usePreviewRoot(t)
			d := dbtest.New(t)
			node := &vfs.Node{ID: 999999}
			msg := peekCmd(ctx, d, node)()
			pm := msg.(previewDoneMsg)
			if pm.err == nil {
				t.Fatal("expected an error for a node with no files")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

// insertVFSEntry inserts one file_registry row and its matching
// virtual_fs_entries proposal row — the minimal fixture vfs.Confirm needs to
// recognize a tree's node IDs as valid target directories.
func insertVFSEntry(t *testing.T, d *db.DB, fileID int64, sourcePath, targetPath string) {
	t.Helper()
	ctx := context.Background()
	if _, err := d.ExecContext(ctx, `
		INSERT INTO file_registry (id, file_dir, file_name, file_size, file_modified_at,
			file_extension, media_type, discovered_at, last_seen_at)
		VALUES (?, '/src', ?, 1024, '2024-06-01T10:00:00.000000000Z', '.jpg', 'IMAGE',
			'2024-06-01T10:00:00.000000000Z', '2024-06-01T10:00:00.000000000Z')`,
		fileID, filepath.Base(sourcePath)); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedEntry(t, d, fileID, sourcePath, targetPath, db.StatusProposed)
}

// dbTree is the review tree BuildTree reads from d.
func dbTree(t *testing.T, d *db.DB) []vfs.Node {
	t.Helper()
	tree, err := vfs.BuildTree(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// nodeAt is the ID of the folder at path p in d's review tree.
func nodeAt(t *testing.T, d *db.DB, p string) int64 {
	t.Helper()
	nodes := dbTree(t, d)
	var found *vfs.Node
	for _, name := range strings.Split(p, "/") {
		found = nil
		for i := range nodes {
			if nodes[i].Name == name {
				found = &nodes[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("no folder %q in the review tree", p)
		}
		nodes = found.Children
	}
	return found.ID
}

// pathIDs gives every folder path a hand-built test tree states a stable int
// ID, so the tree still reads as paths.
var pathIDs = map[string]int64{}

func pid(path string) int64 {
	if id, ok := pathIDs[path]; ok {
		return id
	}
	id := int64(len(pathIDs) + 1)
	pathIDs[path] = id
	return id
}

// siblingTree has two true siblings ("03", "09") under the same parent
// ("June"), the shape a real merge (two date-fallback clusters that turn out
// to be the same place) actually happens on.
func siblingTree() []vfs.Node {
	return []vfs.Node{{ID: pid("2024"), Name: "2024", Children: []vfs.Node{
		{ID: pid("2024/June"), Name: "June", Children: []vfs.Node{
			{ID: pid("2024/June/03"), Name: "03", FileCount: 1},
			{ID: pid("2024/June/09"), Name: "09", FileCount: 1},
		}},
	}}}
}

// nodeByID finds a row by its node ID, however the merge reshuffled row order.
func nodeByID(rows []*reviewRow, id int64) *reviewRow {
	for _, r := range rows {
		if r.node.ID == id {
			return r
		}
	}
	return nil
}

// crossBranchTree mirrors the real reported case: the same device's photos
// spread across three different months, each its own single-file leaf.
func crossBranchTree() []vfs.Node {
	leaf := func(id, name string) vfs.Node { return vfs.Node{ID: pid(id), Name: name, FileCount: 1} }
	return []vfs.Node{{ID: pid("2017"), Name: "2017", Children: []vfs.Node{
		{ID: pid("2017/April"), Name: "April", Children: []vfs.Node{
			{ID: pid("2017/April/20"), Name: "20", Children: []vfs.Node{
				leaf("2017/April/20/Canon EOS 700D", "Canon EOS 700D"),
			}},
		}},
		{ID: pid("2017/August"), Name: "August", Children: []vfs.Node{
			{ID: pid("2017/August/15"), Name: "15", Children: []vfs.Node{
				leaf("2017/August/15/Canon EOS 700D", "Canon EOS 700D"),
			}},
		}},
		{ID: pid("2017/October"), Name: "October", Children: []vfs.Node{
			{ID: pid("2017/October/19"), Name: "19", Children: []vfs.Node{
				leaf("2017/October/19/Canon EOS 700D", "Canon EOS 700D"),
			}},
		}},
	}}}
}

// groupedTree is the reported shape: every month grouped by location and then
// by device, where the device (and location) level is the same everywhere and
// the reviewer doesn't want it.
func groupedTree() []vfs.Node {
	month := func(name string, n int) vfs.Node {
		return vfs.Node{ID: pid("2023/" + name), Name: name, FileCount: n, Children: []vfs.Node{
			{ID: pid("2023/" + name + "/Indore"), Name: "Indore", FileCount: n, Children: []vfs.Node{
				{ID: pid("2023/" + name + "/Indore/Apple iPhone 13"), Name: "Apple iPhone 13", FileCount: n},
			}},
		}}
	}
	return []vfs.Node{{ID: pid("2023"), Name: "2023", FileCount: 13, Children: []vfs.Node{
		month("April", 10), month("August", 3),
	}}}
}

// tripTree is three days of one trip: same location every day, mostly the same
// device — the shape a parent (Day) merge has to handle.
func tripTree() []vfs.Node {
	day := func(d, device string) vfs.Node {
		base := "2024/06_June/" + d
		return vfs.Node{ID: pid(base), Name: d, FileCount: 1, Children: []vfs.Node{
			{ID: pid(base + "/Goa"), Name: "Goa", FileCount: 1, Children: []vfs.Node{
				{ID: pid(base + "/Goa/" + device), Name: device, FileCount: 1},
			}},
		}}
	}
	return []vfs.Node{{ID: pid("2024"), Name: "2024", FileCount: 3, Children: []vfs.Node{
		{ID: pid("2024/06_June"), Name: "06_June", FileCount: 3, Children: []vfs.Node{
			day("03", "iPhone"), day("04", "iPhone"), day("05", "Canon"),
		}},
	}}}
}

// dayWithLocationsTree is one Day holding several locations, each split
// further by device/orientation — the shape [V] + [D] is for.
func dayWithLocationsTree() []vfs.Node {
	loc := func(name string, n int) vfs.Node {
		base := "2024/06_June/03/" + name
		return vfs.Node{ID: pid(base), Name: name, FileCount: n, Children: []vfs.Node{
			{ID: pid(base + "/iPhone"), Name: "iPhone", FileCount: n, Children: []vfs.Node{
				{ID: pid(base + "/iPhone/Vertical"), Name: "Vertical", FileCount: n},
			}},
		}}
	}
	return []vfs.Node{{ID: pid("2024"), Name: "2024", FileCount: 6, Children: []vfs.Node{
		{ID: pid("2024/06_June"), Name: "06_June", FileCount: 6, Children: []vfs.Node{
			{ID: pid("2024/06_June/03"), Name: "03", FileCount: 6, Children: []vfs.Node{
				loc("Goa", 1), loc("Panaji", 2), loc("Margao", 3),
			}},
		}},
	}}}
}

// TestDraft covers the edit journal from the review's side (spec D17/D18):
// every edit lands in the draft file as it is made, reopening replays it,
// leaving asks nothing, and [R] throws the file away without touching the
// database.
func TestDraft(t *testing.T) {
	key := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

	newDBModel := func(t *testing.T, dir string) (Model, []vfs.Node) {
		t.Helper()
		ctx := context.Background()
		d := dbtest.New(t)
		insertVFSEntry(t, d, 1, "/src/a.jpg", "2024/06_June/03/a.jpg")
		tree, err := vfs.BuildTree(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		edits, err := vfs.ReadDraft(dir)
		if err != nil {
			t.Fatal(err)
		}
		return newModel(tree, edits, ctx, d, nil, logger.NewNoopLogger(), dir), tree
	}
	dayRow := func(m Model) *reviewRow {
		for _, r := range m.rows {
			if r.depth == 2 {
				return r
			}
		}
		t.Fatal("no day row")
		return nil
	}

	t.Run("edits survive a reopen", func(t *testing.T) {
		dir := t.TempDir()
		m, _ := newDBModel(t, dir)
		m.focusNode(dayRow(m).node.ID)
		m.applyRename("Goa Trip")
		if edits, _ := vfs.ReadDraft(dir); len(edits) != 1 || edits[0].Op != vfs.OpRename || edits[0].To != "Goa Trip" {
			t.Fatalf("draft = %+v, want one rename to Goa Trip", edits)
		}
		// a kill here is the same as a clean exit: the file is all there is
		again, _ := newDBModel(t, dir)
		if got := dayRow(again).node.Name; got != "Goa Trip" {
			t.Errorf("reopened day = %q, want the rename replayed", got)
		}
	})

	t.Run("undo drops the last line", func(t *testing.T) {
		dir := t.TempDir()
		m, _ := newDBModel(t, dir)
		m.focusNode(dayRow(m).node.ID)
		m.applyRename("One")
		m.applyRename("Two")
		next, _ := m.Update(key('u'))
		m = next.(Model)
		if got := dayRow(m).node.Name; got != "One" {
			t.Errorf("after undo day = %q, want One", got)
		}
		if edits, _ := vfs.ReadDraft(dir); len(edits) != 1 || edits[0].To != "One" {
			t.Errorf("draft after undo = %+v, want only the first rename", edits)
		}
	})

	t.Run("leaving asks nothing and keeps the file", func(t *testing.T) {
		for _, k := range []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyCtrlC}} {
			dir := t.TempDir()
			m, _ := newDBModel(t, dir)
			m.focusNode(dayRow(m).node.ID)
			m.applyRename("Kept")
			next, cmd := m.Update(k)
			if !next.(Model).done || cmd == nil {
				t.Fatalf("%s: want the review to hand back at once", k)
			}
			if _, ok := cmd().(tui.SwitchMsg); !ok {
				t.Errorf("%s: want a tui.SwitchMsg back to the shell", k)
			}
			if edits, _ := vfs.ReadDraft(dir); len(edits) != 1 {
				t.Errorf("%s: draft = %+v, want the edit kept", k, edits)
			}
		}
	})

	t.Run("R deletes the file and shows the plan as proposed", func(t *testing.T) {
		dir := t.TempDir()
		m, base := newDBModel(t, dir)
		proposed := dayRow(m).node.Name
		m.focusNode(dayRow(m).node.ID)
		m.applyRename("Renamed")
		m.visualMode = true
		next, _ := m.Update(key('R'))
		got := next.(Model)
		switch {
		case len(got.edits) != 0:
			t.Error("reset must drop the in-memory edits")
		case dayRow(got).node.Name != proposed:
			t.Errorf("day = %q, want %q back", dayRow(got).node.Name, proposed)
		case got.cursor != 0 || got.visualMode:
			t.Error("cursor and selection must reset onto the proposed tree")
		case vfs.FindNode(base, dayRow(got).node.ID).Name != proposed:
			t.Error("the plan the review was opened on must never be edited in place")
		}
		if _, err := os.Stat(filepath.Join(dir, vfs.DraftFileName)); !os.IsNotExist(err) {
			t.Errorf("draft file still there after [R]: %v", err)
		}
		if strings.Contains(got.keyHelp(), "rebuild") {
			t.Error("the key bar should never mention rebuilding")
		}
	})

	t.Run("no copy or move key", func(t *testing.T) {
		m, _ := newDBModel(t, t.TempDir())
		m.showHelp = true
		help := m.helpView()
		for _, s := range []string{m.keyHelp(), help} {
			if strings.Contains(s, "copy approved") || strings.Contains(s, "move approved") {
				t.Errorf("review must offer no transfer key, got %q", s)
			}
		}
	})
}
