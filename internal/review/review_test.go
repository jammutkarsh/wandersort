// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/location"
)

func sampleTree() []vfs.Node {
	return []vfs.Node{{ID: "2024", Name: "2024", Children: []vfs.Node{
		{ID: "2024/June", Name: "June", FileCount: 1},
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
			m := newModel(sampleTree(), nil, nil, nil, nil)

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
			rm := next.(Model)

			if !rm.previewing {
				t.Error("expected previewing = true right after pressing p")
			}
			if cmd == nil {
				t.Error("expected a non-nil Cmd")
			}
		}},
		// TestPreviewDoneCachesBySignature covers the write side of the cache: a
		// successful copy is remembered under its file-membership signature, not the
		// node it happened to be peeked from.
		{"PreviewDoneCachesBySignature", func(t *testing.T) {
			m := newModel(sampleTree(), nil, nil, nil, nil)

			next, _ := m.Update(previewDoneMsg{signature: "a.jpg\x00b.jpg", dir: "/tmp/wandersort-preview-xyz"})
			rm := next.(Model)

			if rm.previewDirs["a.jpg\x00b.jpg"] != "/tmp/wandersort-preview-xyz" {
				t.Errorf("previewDirs = %+v, want the copied dir cached under the signature", rm.previewDirs)
			}
		}},
		// TestFilesSignatureDedupesParentAndLeafNode covers the actual reported bug:
		// a folder with one child chain (e.g. .../08/Horizontal/Photos) and its leaf
		// both cover the exact same underlying files — peeking either must resolve
		// to the same signature so the same temp copy gets reused.
		{"FilesSignatureDedupesParentAndLeafNode", func(t *testing.T) {
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
				if _, err := d.ExecContext(ctx, `
			INSERT INTO virtual_fs_entries (file_id, source_path, target_path, status)
			VALUES (?, ?, ?, 'PROPOSED')`,
					fileID, "/src/"+name, target); err != nil {
					t.Fatal(err)
				}
			}

			parentFiles, err := vfs.FilesUnder(ctx, "2017/April/08", d)
			if err != nil {
				t.Fatal(err)
			}
			leafFiles, err := vfs.FilesUnder(ctx, "2017/April/08/Horizontal/Photos", d)
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
		}},
		// TestCleanupPreviewDirsRemovesEverything covers the exit-time sweep: every
		// temp dir a review session created must be gone once it's over, regardless
		// of how the reviewer exited (confirm, quit, ctrl-c all funnel through this).
		{"CleanupPreviewDirsRemovesEverything", func(t *testing.T) {
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
		}},
		// TestMergeWithoutVisualModeIsRejectedLoudly covers the reported "merge
		// doesn't work" complaint: pressing m without having pressed V first (e.g.
		// typed lowercase v, which matches no keybinding) must be an obvious warning,
		// not a message indistinguishable from routine dim status text.
		{"MergeWithoutVisualModeIsRejectedLoudly", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil)

			m.mergeSelection()

			if m.statusMsg == "" || !m.statusIsErr {
				t.Errorf("statusMsg = %q, statusIsErr = %v, want a flagged error asking to press V first", m.statusMsg, m.statusIsErr)
			}
		}},
		// TestMergeSingleRowIsRejected covers pressing m right after V with no
		// cursor movement — only one row "selected", nothing to merge.
		{"MergeSingleRowIsRejected", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil)
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
			m := newModel(siblingTree(), nil, nil, nil, nil)
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
			m := newModel(siblingTree(), nil, nil, nil, nil)
			// rows: 0=2024, 1=June, 2=03, 3=09 — select rows 2 and 3
			m.visualMode = true
			m.visualAnchor = 2
			m.cursor = 3

			m.mergeSelection()

			if m.statusIsErr {
				t.Fatalf("expected success, got error status: %q", m.statusMsg)
			}
			r03 := nodeByID(m.rows, "2024/June/03")
			// the merged folder keeps the first pick's own name, so there is nothing
			// to rename it to — newName stays empty rather than restating "03"
			if r03 == nil || r03.node.Name != "03" || r03.newName != "" {
				t.Fatalf("want the surviving node still named %q with no pending rename, got %+v", "03", r03)
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
			if len(m.undo) != 1 {
				t.Errorf("undo stack = %d deep, want 1 step recorded", len(m.undo))
			}
		}},
		// TestMergeAcrossBranchesCollapsesToOneNode covers the real reported case:
		// merging the same camera's leaves out of three different Month/Day branches
		// must leave exactly ONE folder under the Year holding all the files — not
		// three same-named siblings next to three now-empty Month/Day chains, which
		// is what the reviewer sees as "merge didn't work" even though Confirm would
		// have collapsed the paths later.
		{"MergeAcrossBranchesCollapsesToOneNode", func(t *testing.T) {
			m := newModel(crossBranchTree(), nil, nil, nil, nil)
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

			year := vfs.FindNode(m.tree, "2017")
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
				if n := vfs.FindNode(m.tree, "2017/"+month); n != nil {
					t.Errorf("%s should have been pruned — nothing left under it", month)
				}
			}
		}},
		// TestMergeAcrossBranchesRejectsWithNoCommonAncestor covers selecting leaves
		// that share literally no ancestor (different years) — nothing sensible to
		// reparent under.
		{"MergeAcrossBranchesRejectsWithNoCommonAncestor", func(t *testing.T) {
			tree := []vfs.Node{
				{ID: "2017", Name: "2017", Children: []vfs.Node{{ID: "2017/Camera", Name: "Camera", FileCount: 1}}},
				{ID: "2018", Name: "2018", Children: []vfs.Node{{ID: "2018/Camera", Name: "Camera", FileCount: 1}}},
			}
			m := newModel(tree, nil, nil, nil, nil)
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
			m := newModel(groupedTree(), nil, nil, nil, nil)
			// rows: 0=2023, 1=April, 2=Indore, 3=iPhone, 4=August, 5=Indore, 6=iPhone
			m.cursor = 1 // April
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
			rm := next.(Model)
			if rm.statusIsErr {
				t.Fatalf("flatten April: %q", rm.statusMsg)
			}

			april := vfs.FindNode(rm.tree, "2023/April")
			if april == nil || len(april.Children) != 0 {
				t.Fatalf("April = %+v, want a childless node", april)
			}
			if april.FileCount != 10 {
				t.Errorf("April FileCount = %d, want 10 (unchanged — it already counted the subtree)", april.FileCount)
			}
			// both dropped levels must remap, or the files in the deepest one keep
			// their old target_path when Confirm runs
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
			// August is untouched — [D] acts on the cursor's subtree, nothing else
			if aug := vfs.FindNode(rm.tree, "2023/August/Indore/Apple iPhone 13"); aug == nil {
				t.Error("August's subtree should be untouched by a flatten on April")
			}
		}},
		// TestFlattenWorksOnATopLevelRow covers the difference from [d]: flattening a
		// Year keeps the Year itself, so the files have somewhere to go.
		{"FlattenWorksOnATopLevelRow", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil)
			m.cursor = 0 // 2023

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
			rm := next.(Model)

			if rm.statusIsErr {
				t.Fatalf("expected success, got %q", rm.statusMsg)
			}
			if len(rm.rows) != 1 || rm.rows[0].node.ID != "2023" || rm.rows[0].node.FileCount != 13 {
				t.Fatalf("rows = %+v, want just 2023 holding all 13 files", rm.rows)
			}
			if len(rm.tree[0].MergedIDs) != 6 {
				t.Errorf("MergedIDs = %v, want all six descendants", rm.tree[0].MergedIDs)
			}
		}},
		// TestFlattenLeafIsRejected covers the no-op guard.
		{"FlattenLeafIsRejected", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil)
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
			m := newModel(groupedTree(), nil, nil, nil, nil)
			m.cursor = 2 // April's Indore, which still has the device child
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			rm := next.(Model)

			if rm.statusIsErr {
				t.Fatalf("expected success, got %q", rm.statusMsg)
			}
			april := vfs.FindNode(rm.tree, "2023/April")
			if len(april.Children) != 1 || april.Children[0].Name != "Apple iPhone 13" {
				t.Fatalf("April children = %+v, want the lifted device node", april.Children)
			}
			if vfs.FindNode(rm.tree, "2023/August/Indore") == nil {
				t.Error("[d] dropped more than the cursor's folder — August's Indore should be untouched")
			}
			if got := april.MergedIDs; len(got) != 1 || got[0] != "2023/April/Indore" {
				t.Errorf("MergedIDs = %v, want just the dropped folder", got)
			}
		}},
		// TestDropTopLevelIsRejected covers the guard: a Year has no parent to lift
		// files into, so dropping it would dump them in the library root.
		{"DropTopLevelIsRejected", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil)
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
			m := newModel(groupedTree(), nil, nil, nil, nil)
			m.cursor = 1
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
			rm := next.(Model)

			next2, _ := rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
			rm2 := next2.(Model)

			if vfs.FindNode(rm2.tree, "2023/April/Indore/Apple iPhone 13") == nil {
				t.Error("April's subtree should be back after undo")
			}
			if april := vfs.FindNode(rm2.tree, "2023/April"); len(april.MergedIDs) != 0 {
				t.Errorf("undo left MergedIDs behind: %v", april.MergedIDs)
			}
		}},
		// TestUndoRestoresTreeAfterCrossBranchMerge covers [u] on the new structural
		// merge: it must restore the whole pre-merge tree (the old per-row newName
		// undo can't undo a reparent), and stops cleanly once the stack is empty.
		{"UndoRestoresTreeAfterCrossBranchMerge", func(t *testing.T) {
			m := newModel(crossBranchTree(), nil, nil, nil, nil)
			m.visualAnchor = 3
			m.cursor = 9
			m.visualMode = true
			m.mergeSelection()
			if m.statusIsErr {
				t.Fatalf("merge failed: %q", m.statusMsg)
			}

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
			rm := next.(Model)

			april20 := vfs.FindNode(rm.tree, "2017/April/20")
			if april20 == nil || len(april20.Children) != 1 || april20.Children[0].Name != "Canon EOS 700D" {
				t.Errorf("2017/April/20 should have its Canon EOS 700D leaf back after undo, got %+v", april20)
			}
			year := vfs.FindNode(rm.tree, "2017")
			for _, c := range year.Children {
				if c.Name == "Canon EOS 700D" {
					t.Error("2017 should not have a direct Canon EOS 700D child after undo")
				}
			}
			if len(rm.undo) != 0 {
				t.Error("undo stack should be empty again after undoing the only edit")
			}

			// pressing u again with nothing pending must be a no-op, not a crash
			next2, _ := rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
			rm2 := next2.(Model)
			if len(rm2.undo) != 0 {
				t.Error("second undo should still be a no-op")
			}
		}},
		// TestVisibleRowsShrinksWhenHelpWraps covers the reported overflow: on a
		// narrow terminal the key help wraps to several lines, and the tree budget has
		// to shrink by exactly that much or the bottom rows run off the screen.
		{"VisibleRowsShrinksWhenHelpWraps", func(t *testing.T) {
			wide := newModel(sampleTree(), nil, nil, nil, nil)
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
		// TestQuitWarnsOnceBeforeDiscardingEdits covers the reported "no way to save":
		// [c] saves, [q] throws everything away — so [q] with pending edits must warn
		// first, and only quit if the reviewer insists.
		{"QuitWarnsOnceBeforeDiscardingEdits", func(t *testing.T) {
			m := newModel(sampleTree(), nil, nil, nil, nil)
			m.rows[1].newName = "Manali"

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
			rm := next.(Model)
			if cmd != nil {
				t.Fatal("first q with unsaved edits should not quit")
			}
			if !rm.statusIsErr || rm.statusMsg == "" {
				t.Errorf("want a flagged unsaved-changes warning, got %q", rm.statusMsg)
			}

			if _, cmd := rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
				t.Error("second q should quit")
			}

			// any other key in between resets the warning — no accidental quit later
			moved, _ := rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
			if _, cmd := moved.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd != nil {
				t.Error("q after moving should warn again, not quit")
			}
		}},
		// TestQuitWithNoEditsExitsImmediately covers the other side: nothing typed,
		// nothing to lose, no nagging.
		{"QuitWithNoEditsExitsImmediately", func(t *testing.T) {
			m := newModel(sampleTree(), nil, nil, nil, nil)
			if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
				t.Error("q with a clean tree should quit straight away")
			}
		}},
		// TestStructuralEditKeepsPendingRenames covers a silent data-loss bug: merge,
		// delete and undo all rebuild the row list with flattenTree, which allocates
		// fresh reviewRow values. Without reflow carrying newName across by node ID,
		// every rename the reviewer typed on rows the edit never touched vanished.
		{"StructuralEditKeepsPendingRenames", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil)
			// rows: 0=2024, 1=June, 2=03, 3=09. Rename the year, then merge the leaves.
			nodeByID(m.rows, "2024").newName = "Two Thousand Twenty Four"

			m.visualMode, m.visualAnchor, m.cursor = true, 2, 3
			m.mergeSelection()

			if m.statusIsErr {
				t.Fatalf("merge failed: %q", m.statusMsg)
			}
			if got := nodeByID(m.rows, "2024").newName; got != "Two Thousand Twenty Four" {
				t.Errorf("after merge, year newName = %q, want the rename to survive", got)
			}
		}},
		// TestUndoKeepsPendingRenames is the undo half of the same bug.
		{"UndoKeepsPendingRenames", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil)
			nodeByID(m.rows, "2024").newName = "Renamed Year"
			m.visualMode, m.visualAnchor, m.cursor = true, 2, 3
			m.mergeSelection()

			next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
			rm := next.(Model)
			if got := nodeByID(rm.rows, "2024").newName; got != "Renamed Year" {
				t.Errorf("after undo, year newName = %q, want %q", got, "Renamed Year")
			}
			if nodeByID(rm.rows, "2024/June/09") == nil {
				t.Error("undo should have restored the folded-away leaf")
			}
		}},
		// TestRefreshSuggestionsFiltersInMemory covers that typing never touches the
		// DB or the resolver: both sources are pre-loaded, and each keystroke only
		// narrows what's already in memory. A nil db/resolver here would panic or
		// return nothing if refreshSuggestions still queried.
		{"RefreshSuggestionsFiltersInMemory", func(t *testing.T) {
			m := newModel(sampleTree(), context.Background(), nil, nil, nil)
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
				names = append(names, s.label)
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
			m := newModel(sampleTree(), context.Background(), nil, nil, nil)
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
			if len(rm.suggestions) != 1 || rm.suggestions[0].label != "Manali" {
				t.Errorf("suggestions = %+v, want the pre-loaded label filtered in memory", rm.suggestions)
			}
		}},
		// TestMergingParentsCollapsesSameNamedChildren covers the reported case:
		// merging three Day folders of one trip must leave a single Goa underneath,
		// not three the reviewer then has to merge by hand. Children that genuinely
		// differ (a second camera) stay separate.
		{"MergingParentsCollapsesSameNamedChildren", func(t *testing.T) {
			m := newModel(tripTree(), nil, nil, nil, nil)
			// rows: 0=2024 1=06_June 2=03 3=Goa 4=iPhone 5=04 6=Goa 7=iPhone 8=05 9=Goa 10=Canon
			m.visualAnchor, m.cursor, m.visualMode = 2, 10, true

			m.mergeSelection()

			if m.statusIsErr {
				t.Fatalf("expected success, got %q", m.statusMsg)
			}
			june := vfs.FindNode(m.tree, "2024/06_June")
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
			m := newModel(tripTree(), nil, nil, nil, nil)
			// anchor on 03's Goa (depth 3) through 05's Goa — merges the Goas, not the days
			m.visualAnchor, m.cursor, m.visualMode = 3, 9, true

			m.mergeSelection()

			if m.statusIsErr {
				t.Fatalf("expected success, got %q", m.statusMsg)
			}
			// all three Goas merged under their lowest common ancestor, the month
			june := vfs.FindNode(m.tree, "2024/06_June")
			if len(june.Children) != 1 || june.Children[0].Name != "Goa" {
				t.Fatalf("June children = %+v, want one Goa (the days were emptied and pruned)", june.Children)
			}
		}},
		// TestMergeRespectsPendingRenames covers name matching: children collapse on
		// the name they will actually be written as, not the one they started with.
		{"MergeRespectsPendingRenames", func(t *testing.T) {
			m := newModel(tripTree(), nil, nil, nil, nil)
			m.rows[10].newName = "iPhone" // rename 05's Canon to match the others
			m.visualAnchor, m.cursor, m.visualMode = 2, 10, true

			m.mergeSelection()

			goa := vfs.FindNode(m.tree, "2024/06_June/03/Goa")
			if goa == nil || len(goa.Children) != 1 {
				t.Fatalf("Goa children = %+v, want one — the renamed Canon collapses into iPhone", goa)
			}
		}},
		// TestMergeNeverBroadcastsASuggestion covers the reported surprise: merging
		// folders produced a name nobody chose ("Canon EOS 700D → 19"), because the
		// merged name fell back to a suggestion. A suggestion is an offer the reviewer
		// hasn't accepted — the merge must use the first pick's own name, or the
		// rename they actually typed on it.
		{"MergeNeverBroadcastsASuggestion", func(t *testing.T) {
			tree := []vfs.Node{{ID: "2017", Name: "2017", FileCount: 2, Children: []vfs.Node{
				{ID: "2017/20", Name: "20", FileCount: 1, Suggestions: []vfs.Suggestion{{Name: "Canon EOS 700D"}}},
				{ID: "2017/19", Name: "19", FileCount: 1, Suggestions: []vfs.Suggestion{{Name: "Canon EOS 700D"}}},
			}}}
			m := newModel(tree, nil, nil, nil, nil)
			m.visualAnchor, m.cursor, m.visualMode = 1, 2, true

			m.mergeSelection()

			if m.statusIsErr {
				t.Fatalf("expected success, got %q", m.statusMsg)
			}
			row := nodeByID(m.rows, "2017/20")
			if row == nil {
				t.Fatal("expected the first pick to survive the merge")
			}
			if row.newName != "" {
				t.Errorf("merged folder was renamed to %q — merge must not apply a suggestion", row.newName)
			}
			if row.node.Name != "20" {
				t.Errorf("merged folder name = %q, want the first pick's own name %q", row.node.Name, "20")
			}
		}},
		// TestMergeKeepsAnExplicitRename is the other half: a rename the reviewer
		// typed on the first pick *is* what the merged folder should be called.
		{"MergeKeepsAnExplicitRename", func(t *testing.T) {
			m := newModel(siblingTree(), nil, nil, nil, nil)
			m.rows[2].newName = "Goa Trip"
			m.visualAnchor, m.cursor, m.visualMode = 2, 3, true

			m.mergeSelection()

			row := nodeByID(m.rows, "2024/June/03")
			if row == nil || row.newName != "Goa Trip" {
				t.Fatalf("want the typed rename carried onto the merged folder, got %+v", row)
			}
		}},
		// TestUndoWalksBackThroughEveryEdit covers multi-level undo: several
		// structural edits in a row must each be reversible, in order, all the way to
		// the tree the review started with.
		{"UndoWalksBackThroughEveryEdit", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil)
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

			if len(rm.undo) != 3 {
				t.Fatalf("undo stack = %d deep, want 3", len(rm.undo))
			}
			for i := 3; i > 0; i-- {
				next, _ = rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
				rm = next.(Model)
				if len(rm.undo) != i-1 {
					t.Fatalf("after undo %d: stack = %d deep, want %d", 4-i, len(rm.undo), i-1)
				}
				if rm.statusIsErr {
					t.Fatalf("undo %d reported an error: %q", 4-i, rm.statusMsg)
				}
			}

			if len(rm.rows) != before {
				t.Errorf("%d rows after undoing everything, want the original %d", len(rm.rows), before)
			}
			if vfs.FindNode(rm.tree, "2023/April/Indore/Apple iPhone 13") == nil {
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
			m := newModel(dayWithLocationsTree(), nil, nil, nil, nil)
			// rows: 0=2024 1=06_June 2=03 3=Goa 4=iPhone 5=Vertical 6=Panaji ... 9=Margao ...
			m.visualAnchor, m.cursor, m.visualMode = 3, 11, true

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
			rm := next.(Model)
			if rm.statusIsErr {
				t.Fatalf("expected success, got %q", rm.statusMsg)
			}

			day := vfs.FindNode(rm.tree, "2024/06_June/03")
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
			if len(rm.undo) != 1 {
				t.Errorf("undo stack = %d, want one step for the whole multi-folder flatten", len(rm.undo))
			}
		}},
		// TestVisualDropActsOnEverySelectedFolder covers [V] + [d]: each selected
		// folder goes and its children are lifted onto the parent they shared.
		{"VisualDropActsOnEverySelectedFolder", func(t *testing.T) {
			m := newModel(dayWithLocationsTree(), nil, nil, nil, nil)
			m.visualAnchor, m.cursor, m.visualMode = 3, 11, true

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			rm := next.(Model)
			if rm.statusIsErr {
				t.Fatalf("expected success, got %q", rm.statusMsg)
			}

			day := vfs.FindNode(rm.tree, "2024/06_June/03")
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
			m := newModel(dayWithLocationsTree(), nil, nil, nil, nil)
			// anchor on Goa (depth 3), extend only into its own subtree
			m.visualAnchor, m.cursor, m.visualMode = 3, 5, true

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
			rm := next.(Model)

			if goa := vfs.FindNode(rm.tree, "2024/06_June/03/Goa"); len(goa.Children) != 0 {
				t.Error("Goa should be flattened")
			}
			if p := vfs.FindNode(rm.tree, "2024/06_June/03/Panaji"); p == nil || len(p.Children) != 1 {
				t.Error("Panaji was outside the range and must be untouched")
			}
		}},
		// TestJumpSameDepthCrossesBranches covers [n]/[N]: the cursor hops to the next
		// row at its own indent depth wherever that is, so a deep tree is walkable
		// without scrolling through every folder's contents — and [V] plus [n] can
		// select one level across several branches.
		{"JumpSameDepthCrossesBranches", func(t *testing.T) {
			m := newModel(groupedTree(), nil, nil, nil, nil)
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
			m := newModel(groupedTree(), nil, nil, nil, nil)
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
			m := newModel(groupedTree(), nil, nil, nil, nil)
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
				return vfs.Node{ID: "2017/12_December/" + n, Name: n, FileCount: files, Children: kids}
			}
			tree := []vfs.Node{{ID: "2017", Name: "2017", FileCount: 40, Children: []vfs.Node{
				{ID: "2017/12_December", Name: "12_December", FileCount: 40, Children: []vfs.Node{
					day("16", 4), day("20", 12), day("21", 20), day("22", 3), day("25", 1),
				}},
			}}}
			m := newModel(tree, nil, nil, nil, nil)
			// rows: 0=2017 1=12_December 2=16 3=20 4=21 5=22 6=25 — merge 21 and 22
			m.visualAnchor, m.cursor, m.visualMode = 4, 5, true
			m.mergeSelection()

			dec := vfs.FindNode(m.tree, "2017/12_December")
			var names []string
			for _, c := range dec.Children {
				names = append(names, c.Name)
			}
			if !slices.IsSorted(names) {
				t.Errorf("December children = %v, want name order — an appended merge result reads as a deleted folder", names)
			}
			if len(names) != 4 || names[2] != "21" {
				t.Errorf("children = %v, want the merged 21 back in its sorted position", names)
			}
			// and the cursor follows the merged folder rather than staying on an index
			if m.rows[m.cursor].node.ID != "2017/12_December/21" {
				t.Errorf("cursor is on %q, want the merged folder", m.rows[m.cursor].node.ID)
			}
		}},
		// TestDropKeepsNameOrder covers the same for lifted children.
		{"DropKeepsNameOrder", func(t *testing.T) {
			m := newModel(dayWithLocationsTree(), nil, nil, nil, nil)
			m.visualAnchor, m.cursor, m.visualMode = 3, 11, true
			m.dropFolders(m.selectedRows())

			day := vfs.FindNode(m.tree, "2024/06_June/03")
			var ids []string
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
			m := newModel(siblingTree(), nil, nil, nil, nil)
			want := map[string]string{
				"2024":         "",
				"2024/June":    "└─ ",
				"2024/June/03": "   ├─ ",
				"2024/June/09": "   └─ ",
			}
			for id, wantGuide := range want {
				row := nodeByID(m.rows, id)
				if row == nil {
					t.Fatalf("no row for %q", id)
				}
				if row.guide != wantGuide {
					t.Errorf("guide for %q = %q, want %q", id, row.guide, wantGuide)
				}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
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

// nodeByID finds a row by its node ID, however the merge reshuffled row order.
func nodeByID(rows []*reviewRow, id string) *reviewRow {
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

// groupedTree is the reported shape: every month grouped by location and then
// by device, where the device (and location) level is the same everywhere and
// the reviewer doesn't want it.
func groupedTree() []vfs.Node {
	month := func(name string, n int) vfs.Node {
		return vfs.Node{ID: "2023/" + name, Name: name, FileCount: n, Children: []vfs.Node{
			{ID: "2023/" + name + "/Indore", Name: "Indore", FileCount: n, Children: []vfs.Node{
				{ID: "2023/" + name + "/Indore/Apple iPhone 13", Name: "Apple iPhone 13", FileCount: n},
			}},
		}}
	}
	return []vfs.Node{{ID: "2023", Name: "2023", FileCount: 13, Children: []vfs.Node{
		month("April", 10), month("August", 3),
	}}}
}

// tripTree is three days of one trip: same location every day, mostly the same
// device — the shape a parent (Day) merge has to handle.
func tripTree() []vfs.Node {
	day := func(d, device string) vfs.Node {
		base := "2024/06_June/" + d
		return vfs.Node{ID: base, Name: d, FileCount: 1, Children: []vfs.Node{
			{ID: base + "/Goa", Name: "Goa", FileCount: 1, Children: []vfs.Node{
				{ID: base + "/Goa/" + device, Name: device, FileCount: 1},
			}},
		}}
	}
	return []vfs.Node{{ID: "2024", Name: "2024", FileCount: 3, Children: []vfs.Node{
		{ID: "2024/06_June", Name: "06_June", FileCount: 3, Children: []vfs.Node{
			day("03", "iPhone"), day("04", "iPhone"), day("05", "Canon"),
		}},
	}}}
}

// dayWithLocationsTree is one Day holding several locations, each split
// further by device/orientation — the shape [V] + [D] is for.
func dayWithLocationsTree() []vfs.Node {
	loc := func(name string, n int) vfs.Node {
		base := "2024/06_June/03/" + name
		return vfs.Node{ID: base, Name: name, FileCount: n, Children: []vfs.Node{
			{ID: base + "/iPhone", Name: "iPhone", FileCount: n, Children: []vfs.Node{
				{ID: base + "/iPhone/Vertical", Name: "Vertical", FileCount: n},
			}},
		}}
	}
	return []vfs.Node{{ID: "2024", Name: "2024", FileCount: 6, Children: []vfs.Node{
		{ID: "2024/06_June", Name: "06_June", FileCount: 6, Children: []vfs.Node{
			{ID: "2024/06_June/03", Name: "03", FileCount: 6, Children: []vfs.Node{
				loc("Goa", 1), loc("Panaji", 2), loc("Margao", 3),
			}},
		}},
	}}}
}
