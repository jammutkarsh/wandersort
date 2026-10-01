package review

import (
	"fmt"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
)

// undo drops the last edit from the draft; the tree comes back as the plan
// plus the edits left.
func (m *Model) undo() {
	last, err := m.draft.Undo()
	if err != nil {
		m.statusMsg, m.statusIsErr = err.Error(), true
		return
	}
	m.reflow()
	left := ""
	if n := len(m.draft.Edits()); n > 0 {
		left = fmt.Sprintf(" (%d more)", n)
	}
	m.statusMsg, m.statusIsErr = "undid "+last.Op+left, false
}

// applyEdit hands one edit to the draft, then reflows and reports. Returns the
// outcome and whether the edit landed; a refused or unjournalled edit changes
// nothing.
func (m *Model) applyEdit(e vfs.Edit, status func(vfs.Outcome) string) (vfs.Outcome, bool) {
	out, err := m.draft.Apply(e)
	m.reflow()
	if err != nil {
		m.statusMsg, m.statusIsErr = err.Error(), true
		return out, false
	}
	m.visualMode = false
	m.statusMsg, m.statusIsErr = status(out), false
	return out, true
}

// mergeSelection folds the selected folders into one node under their lowest
// common ancestor.
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
	// the row [V] was pressed on names the result, whichever way the range
	// was extended, so move it to the front
	ids := make([]int64, 0, len(sel))
	ids = append(ids, anchorID)
	for _, r := range sel {
		if r.node.ID != anchorID {
			ids = append(ids, r.node.ID)
		}
	}

	if out, ok := m.applyEdit(vfs.Edit{Op: vfs.OpMerge, Nodes: ids}, func(o vfs.Outcome) string {
		return fmt.Sprintf("merged %d folders into %q under %q ([u] to undo)", len(ids), o.Name, o.Parent)
	}); ok {
		m.focusNode(out.Focus)
	}
}

// applyRename writes the name onto the node as an edit, so [u] reverts it.
func (m *Model) applyRename(name string) {
	row := m.rows[m.cursor]
	id, old := row.node.ID, row.node.Name
	if name == "" || name == old {
		return
	}
	if _, ok := m.applyEdit(vfs.Edit{Op: vfs.OpRename, Node: id, From: old, To: name}, func(vfs.Outcome) string {
		return fmt.Sprintf("renamed %q to %q ([u] to undo)", old, name)
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
	// rows deeper than the anchor are its contents and ride along; shallower
	// rows are scaffolding the range spans
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
// parent, one group-by level shallower. The draft does the reshaping.
func (m *Model) dropFolders(targets []*reviewRow) {
	ids := make([]int64, len(targets))
	for i, r := range targets {
		ids[i] = r.node.ID
	}

	m.applyEdit(vfs.Edit{Op: vfs.OpDrop, Nodes: ids}, func(o vfs.Outcome) string {
		names := o.Names
		what := fmt.Sprintf("dropped %q", names[0])
		if len(names) > 1 {
			what = fmt.Sprintf("dropped %d folders", len(names))
		}
		return what + " — their files moved up one level ([u] to undo)"
	})
}

// flattenFolders collapses everything below each selected folder into it,
// the folder itself staying put. The draft does the reshaping.
func (m *Model) flattenFolders(targets []*reviewRow) {
	ids := make([]int64, len(targets))
	for i, r := range targets {
		ids[i] = r.node.ID
	}

	// over a [V] range the folders stay separate — folding them together is
	// [m]'s job, not this one
	m.applyEdit(vfs.Edit{Op: vfs.OpFlatten, Nodes: ids}, func(o vfs.Outcome) string {
		names := o.Names
		into := fmt.Sprintf("%q", names[len(names)-1])
		if len(names) > 1 {
			into = fmt.Sprintf("%d folders", len(names))
		}
		return fmt.Sprintf("flattened %d subfolders into %s ([u] to undo)", o.Absorbed, into)
	})
}
