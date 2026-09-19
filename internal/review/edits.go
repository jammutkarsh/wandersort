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

// record journals one landed edit (spec D17): the draft file first, so an
// edit on screen is one a crash can't lose, then the in-memory copy [u] pops.
// A failed write undoes the edit on screen too — the reviewer would otherwise
// see a change that the next session has never heard of.
func (m *Model) record(e vfs.Edit) {
	e.Seq = len(m.edits) + 1
	if err := vfs.AppendDraft(m.outputDir, e); err != nil {
		m.tree = vfs.Replay(vfs.CloneTree(m.base), m.edits)
		m.reflow()
		m.statusMsg, m.statusIsErr = err.Error(), true
		return
	}
	m.edits = append(m.edits, e)
}

// undo drops the last edit from the draft and rebuilds the tree from the plan
// as proposed plus the edits left — the journal is the whole history, so
// there is no snapshot stack to keep beside it.
func (m *Model) undo() {
	n := len(m.edits)
	if n == 0 {
		m.statusMsg, m.statusIsErr = "nothing left to undo", true
		return
	}
	if err := vfs.WriteDraft(m.outputDir, m.edits[:n-1]); err != nil {
		m.statusMsg, m.statusIsErr = err.Error(), true
		return
	}
	last := m.edits[n-1]
	m.edits = m.edits[:n-1]
	m.tree = vfs.Replay(vfs.CloneTree(m.base), m.edits)
	m.reflow()
	left := ""
	if len(m.edits) > 0 {
		left = fmt.Sprintf(" (%d more)", len(m.edits))
	}
	m.statusMsg, m.statusIsErr = "undid "+last.Op+left, false
}

// applyEdit runs one structural tree edit: apply, reflow, journal, report.
// Returns whether the edit landed, so a caller with follow-up work (merge
// re-focusing the surviving node) knows whether to do it. The edit functions
// validate before they touch the tree, so a refusal leaves it as it was.
func (m *Model) applyEdit(e vfs.Edit, edit func([]vfs.Node) ([]vfs.Node, string, error)) bool {
	newTree, status, err := edit(m.tree)
	if err != nil {
		m.statusMsg, m.statusIsErr = err.Error(), true
		return false
	}
	m.tree = newTree
	m.reflow()
	m.visualMode = false
	m.statusMsg, m.statusIsErr = status, false
	m.record(e)
	return !m.statusIsErr
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
	ids := make([]int64, 0, len(sel))
	ids = append(ids, anchorID)
	for _, r := range sel {
		if r.node.ID != anchorID {
			ids = append(ids, r.node.ID)
		}
	}

	var mergedID int64
	if ok := m.applyEdit(vfs.Edit{Op: vfs.OpMerge, Nodes: ids}, func(tree []vfs.Node) ([]vfs.Node, string, error) {
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
// undo. Journalled like any other edit, so [u] reverts it the same way.
func (m *Model) applyRename(name string) {
	row := m.rows[m.cursor]
	id, old := row.node.ID, row.node.Name
	if name == "" || name == old {
		return
	}
	if ok := m.applyEdit(vfs.Edit{Op: vfs.OpRename, Node: id, From: old, To: name}, func(tree []vfs.Node) ([]vfs.Node, string, error) {
		n := vfs.FindNode(tree, id)
		if n == nil {
			return nil, "", fmt.Errorf("internal error locating %q", old)
		}
		if n.Fixed() {
			return nil, "", vfs.ErrFixedFolder
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
	ids := make([]int64, len(targets))
	for i, r := range targets {
		ids[i] = r.node.ID
	}

	m.applyEdit(vfs.Edit{Op: vfs.OpDrop, Nodes: ids}, func(tree []vfs.Node) ([]vfs.Node, string, error) {
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
	ids := make([]int64, len(targets))
	for i, r := range targets {
		ids[i] = r.node.ID
	}

	// over a [V] range the folders stay separate — folding them together is
	// [m]'s job, not this one
	m.applyEdit(vfs.Edit{Op: vfs.OpFlatten, Nodes: ids}, func(tree []vfs.Node) ([]vfs.Node, string, error) {
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
