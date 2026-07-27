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

// applyEdit runs one structural tree edit: snapshot, apply, roll the snapshot
// back if the edit was refused, then reflow and report. edit returns the new
// tree and the status line for a success. Reports whether the edit landed, so
// a caller with follow-up work (merge, which re-focuses the surviving node)
// knows to do it.
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
// common ancestor, with the summed file count. See selectedRows for what
// counts as selected. The actual reshaping is vfs.MergeNodes' — this only
// resolves the row selection into IDs and applies the result back onto the
// row/undo/status state a tree edit knows nothing about.
func (m *Model) mergeSelection() {
	if !m.visualMode {
		m.statusMsg, m.statusIsErr = "press V to select folders, then m to merge", true
		return
	}
	sel := m.selectedRows()
	m.visualMode = false
	if len(sel) < 2 {
		m.statusMsg, m.statusIsErr = "select at least two folders at the same level to merge", true
		return
	}
	ids := make([]string, len(sel))
	for i, r := range sel {
		ids[i] = r.node.ID
	}

	var mergedID, target string
	ok := m.applyEdit("merge", func(tree []vfs.Node) ([]vfs.Node, string, error) {
		newTree, id, name, ancestor, err := vfs.MergeNodes(tree, ids, m.pendingNames())
		if err != nil {
			return nil, "", err
		}
		mergedID, target = id, name
		return newTree, fmt.Sprintf("merged %d folders into %q under %q ([u] to undo)", len(ids), name, ancestor), nil
	})
	if !ok {
		return
	}
	if row := nodeRowByID(m.rows, mergedID); row != nil && target != row.node.Name {
		row.newName = target
	}
	m.focusNode(mergedID)
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

// pendingNames maps node ID → the rename typed for it, so a merge compares the
// names folders will end up with, not the ones they start with.
func (m *Model) pendingNames() map[string]string {
	pending := map[string]string{}
	for _, r := range m.rows {
		if r.newName != "" {
			pending[r.node.ID] = r.newName
		}
	}
	return pending
}

// dropFolders removes each selected folder and lifts its children onto its
// parent — dropping "Apple iPhone 13" then "Indore" under 2023/April leaves
// April holding the files, one group-by level shallower. vfs.DropNodes does
// the actual reshaping; see mergeSelection's comment for the split.
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

// flattenFolders collapses everything below each selected folder into it, the
// folder itself staying put: `2023/April/Indore/Apple iPhone 13` flattened at
// April becomes `2023/April` holding all ten files. Works on a top-level row,
// unlike [d], since the Year survives to hold them.
//
// Over a [V] range the folders stay separate — folding them together is [m]'s
// job. FileCount is unchanged; it already counted the subtree. vfs.FlattenNodes
// does the actual reshaping; see mergeSelection's comment for the split.
func (m *Model) flattenFolders(targets []*reviewRow) {
	ids := make([]string, len(targets))
	for i, r := range targets {
		ids[i] = r.node.ID
	}

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
