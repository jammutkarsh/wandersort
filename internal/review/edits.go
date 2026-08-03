// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"fmt"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
)

// undoStep is the tree as it stood before one structural edit, plus what that
// edit was, for the status line.
type undoStep struct {
	tree []vfs.Node
	edit string
}

// maxUndo caps how far back [u] walks. A step clones the folder tree only
// (never files), so each is cheap — this just bounds a long session's memory.
const maxUndo = 100

// snapshot records the tree before a structural edit so [u] can walk back to
// it. Called by every edit that reshapes the tree — merge, drop, flatten.
func (m *Model) snapshot(edit string) {
	m.undo = append(m.undo, undoStep{tree: vfs.CloneTree(m.tree), edit: edit})
	if len(m.undo) > maxUndo {
		m.undo = m.undo[len(m.undo)-maxUndo:]
	}
}

// applyEdit runs one structural tree edit: snapshot, apply, reflow, report.
// Returns whether the edit landed, so a caller with follow-up work (merge
// re-focusing the surviving node) knows whether to do it.
func (m *Model) applyEdit(name string, edit func([]vfs.Node) ([]vfs.Node, string, error)) bool {
	m.snapshot(name)
	newTree, status, err := edit(m.tree)
	if err != nil {
		m.undo = m.undo[:len(m.undo)-1] // nothing was mutated, discard the snapshot
		m.statusMsg, m.statusIsErr = err.Error(), true
		return false
	}
	m.tree = newTree
	m.reflow()
	m.visualMode = false
	m.statusMsg, m.statusIsErr = status, false
	return true
}

// mergeSelection folds the selected folders into one node under their lowest
// common ancestor. It only resolves the row selection into IDs — vfs.MergeNodes
// does the actual reshaping.
func (m *Model) mergeSelection() {
	if !m.visualMode {
		m.statusMsg, m.statusIsErr = "press V to select folders, then m to merge", true
		return
	}
	sel := m.selectedRows()
	anchorID := m.rows[m.visualAnchor].node.ID
	m.visualMode = false
	if len(sel) < 2 {
		m.statusMsg, m.statusIsErr = "select at least two folders at the same level to merge", true
		return
	}
	// the row [V] was pressed on names the merged folder, whichever direction
	// the selection was extended in — selectedRows normalizes to tree order,
	// so the anchor has to be pulled back to the front here
	ids := make([]string, 0, len(sel))
	ids = append(ids, anchorID)
	for _, r := range sel {
		if r.node.ID != anchorID {
			ids = append(ids, r.node.ID)
		}
	}

	var mergedID string
	if ok := m.applyEdit("merge", func(tree []vfs.Node) ([]vfs.Node, string, error) {
		newTree, id, name, ancestor, err := vfs.MergeNodes(tree, ids)
		if err != nil {
			return nil, "", err
		}
		mergedID = id
		return newTree, fmt.Sprintf("merged %d folders into %q under %q ([u] to undo)", len(ids), name, ancestor), nil
	}); ok {
		m.focusNode(mergedID)
	}
}

// applyRename writes the name straight onto the node — there is no pending
// rename layer, so nothing is left over to render as an arrow or to survive an
// undo. Snapshots first, so [u] reverts a rename like any other edit.
func (m *Model) applyRename(name string) {
	row := m.rows[m.cursor]
	id, old := row.node.ID, row.node.Name
	if name == "" || name == old {
		return
	}
	if ok := m.applyEdit("rename", func(tree []vfs.Node) ([]vfs.Node, string, error) {
		n := vfs.FindNode(tree, id)
		if n == nil {
			return nil, "", fmt.Errorf("internal error locating %q", old)
		}
		n.Name = name
		return tree, fmt.Sprintf("renamed %q to %q ([u] to undo)", old, name), nil
	}); ok {
		m.focusNode(id) // the re-sort may have moved it
	}
}

// selectedRows are the rows [m]/[d]/[D] act on: in visual mode every row of the
// range at the anchor's depth, otherwise just the row under the cursor.
func (m *Model) selectedRows() []*reviewRow {
	if !m.visualMode {
		return []*reviewRow{m.rows[m.cursor]}
	}
	lo, hi := m.visualAnchor, m.cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	// anchor depth is the rule: deeper rows are the selected folders' own
	// contents and ride along, shallower ones are scaffolding the range spanned
	// to reach the next branch
	depth := m.rows[m.visualAnchor].depth
	var out []*reviewRow
	for _, r := range m.rows[lo : hi+1] {
		if r.depth == depth {
			out = append(out, r)
		}
	}
	return out
}

// dropFolders removes each selected folder and lifts its children onto its
// parent, one group-by level shallower. vfs.DropNodes does the reshaping.
func (m *Model) dropFolders(targets []*reviewRow) {
	ids := make([]string, len(targets))
	for i, r := range targets {
		ids[i] = r.node.ID
	}

	m.applyEdit("drop", func(tree []vfs.Node) ([]vfs.Node, string, error) {
		newTree, names, err := vfs.DropNodes(tree, ids)
		if err != nil {
			return nil, "", err
		}
		what := fmt.Sprintf("dropped %q", names[0])
		if len(names) > 1 {
			what = fmt.Sprintf("dropped %d folders", len(names))
		}
		return newTree, what + " — their files moved up one level ([u] to undo)", nil
	})
}

// flattenFolders collapses everything below each selected folder into it,
// the folder itself staying put. vfs.FlattenNodes does the reshaping.
func (m *Model) flattenFolders(targets []*reviewRow) {
	ids := make([]string, len(targets))
	for i, r := range targets {
		ids[i] = r.node.ID
	}

	// over a [V] range the folders stay separate — folding them together is
	// [m]'s job, not this one
	m.applyEdit("flatten", func(tree []vfs.Node) ([]vfs.Node, string, error) {
		newTree, absorbed, names, err := vfs.FlattenNodes(tree, ids)
		if err != nil {
			return nil, "", err
		}
		into := fmt.Sprintf("%q", names[len(names)-1])
		if len(names) > 1 {
			into = fmt.Sprintf("%d folders", len(names))
		}
		return newTree, fmt.Sprintf("flattened %d subfolders into %s ([u] to undo)", absorbed, into), nil
	})
}
