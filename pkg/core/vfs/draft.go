// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/jammutkarsh/wandersort/pkg/atomicfile"
	"github.com/jammutkarsh/wandersort/pkg/db"
)

// DraftFileName is the review's edit journal (spec D17), next to the database:
// one JSON line per edit, appended and synced as the reviewer makes it. The
// database only ever holds the plan as proposed; the draft is everything the
// reviewer did to it since, so a crash mid-review loses nothing and edits
// never bloat the database. Draft is the only reader and writer; ApplyDraft
// writes it into the plan.
const DraftFileName = ".wandersort.draft"

// Edit ops, as written to the draft.
const (
	OpRename  = "rename"
	OpMerge   = "merge"
	OpDrop    = "drop"
	OpFlatten = "flatten"
)

// Edit is one review edit. Rename names one Node (From is for the reader of
// the file; replay only needs To). Merge, drop and flatten list Nodes — merge
// anchor first, so the replay keeps the same survivor; drop and flatten every
// folder of a [V] range, since the range is one edit and one [u].
type Edit struct {
	Seq   int     `json:"seq"`
	Op    string  `json:"op"`
	Node  int64   `json:"node,omitzero"`
	Nodes []int64 `json:"nodes,omitempty"`
	From  string  `json:"from,omitempty"`
	To    string  `json:"to,omitempty"`
}

func draftPath(outputDir string) string { return filepath.Join(outputDir, DraftFileName) }

// readDraft returns the journalled edits in order; no file is no edits. A
// torn last line (a crash mid-append) is dropped, and the file rewritten
// without it: that edit never finished being recorded, and leaving the
// fragment would glue the next append onto it — one broken line, the new
// edit lost, and the one after that an error every later read hits. A bad
// line anywhere else is an error — skipping it would replay the rest onto a
// tree it was never made against.
func readDraft(outputDir string) ([]Edit, error) {
	data, err := os.ReadFile(draftPath(outputDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read review draft: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
	var edits []Edit
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		var e Edit
		if err := json.Unmarshal(line, &e); err != nil {
			if i == len(lines)-1 {
				if err := writeDraft(outputDir, edits); err != nil {
					return nil, err
				}
				break
			}
			return nil, fmt.Errorf("review draft line %d: %w", i+1, err)
		}
		edits = append(edits, e)
	}
	// A whole last edit with no newline after it (a crash between the two)
	// reads fine, but the next append would glue onto it just the same.
	if data[len(data)-1] != '\n' {
		if err := writeDraft(outputDir, edits); err != nil {
			return nil, err
		}
	}
	return edits, nil
}

// appendDraft records one edit and syncs it to disk before returning, so an
// edit on screen is an edit that survives a crash.
func appendDraft(outputDir string, e Edit) error {
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode review edit: %w", err)
	}
	_, statErr := os.Stat(draftPath(outputDir))
	f, err := os.OpenFile(draftPath(outputDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open review draft: %w", err)
	}
	w := bufio.NewWriter(f)
	w.Write(line)
	w.WriteByte('\n')
	if err := errors.Join(w.Flush(), f.Sync(), f.Close()); err != nil {
		return fmt.Errorf("write review draft: %w", err)
	}
	// The first edit creates the file, and a synced file whose folder entry
	// never reached the disk is lost with it after a power cut.
	if os.IsNotExist(statErr) {
		if err := atomicfile.SyncDir(outputDir); err != nil {
			return fmt.Errorf("write review draft: %w", err)
		}
	}
	return nil
}

// writeDraft replaces the whole journal with edits — [u] drops the last line
// this way. Written beside the file and renamed over it, so a crash leaves
// either the old journal or the new one. No edits removes the file.
func writeDraft(outputDir string, edits []Edit) error {
	if len(edits) == 0 {
		return RemoveDraft(outputDir)
	}
	var buf bytes.Buffer
	for _, e := range edits {
		line, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("encode review edit: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	tmp := draftPath(outputDir) + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("write review draft: %w", err)
	}
	_, werr := f.Write(buf.Bytes())
	if err := errors.Join(werr, f.Sync(), f.Close()); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write review draft: %w", err)
	}
	if err := os.Rename(tmp, draftPath(outputDir)); err != nil {
		return fmt.Errorf("write review draft: %w", err)
	}
	if err := atomicfile.SyncDir(outputDir); err != nil {
		return fmt.Errorf("write review draft: %w", err)
	}
	return nil
}

// RemoveDraft throws every unapplied edit away: [R] reset, a new proposal
// (Propose — its folder IDs are not the ones the edits name), and ApplyDraft
// once the edits are in the database.
func RemoveDraft(outputDir string) error {
	if err := os.Remove(draftPath(outputDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove review draft: %w", err)
	}
	return nil
}

// Draft is the review's edit session over one proposal (spec D17): the tree
// as proposed, the journal of edits on disk, and the tree those edits make.
// Every edit — made on screen or replayed from the file — goes through one
// dispatch (applyEdit), so the review and ApplyDraft can never disagree about
// what an edit does or which edits are refused.
type Draft struct {
	dir   string
	base  []Node // the plan as the database holds it, never edited
	edits []Edit
	tree  []Node // base with edits replayed
}

// ErrNothingToUndo is Undo on a draft with no edits left.
var ErrNothingToUndo = errors.New("nothing left to undo")

// OpenDraft reads the journal in outputDir and replays it onto base. base is
// never edited: the draft works on its own copy, which is what lets Undo and
// Reset go back to it.
func OpenDraft(outputDir string, base []Node) (*Draft, error) {
	edits, err := readDraft(outputDir)
	if err != nil {
		return nil, err
	}
	return &Draft{dir: outputDir, base: base, edits: edits, tree: replay(cloneTree(base), edits)}, nil
}

// Tree is the plan with every edit applied, sorted by name.
func (d *Draft) Tree() []Node { return d.tree }

// Edits are the journalled edits, in order.
func (d *Draft) Edits() []Edit { return slices.Clone(d.edits) }

// Outcome is what an applied edit did, for the reviewer's status line.
type Outcome struct {
	Focus    int64    // the folder the edit leaves in view: the renamed one, the merge's survivor
	Name     string   // rename: the new name; merge: the survivor's name
	Parent   string   // merge: the common folder the survivor now sits under
	Names    []string // drop, flatten: the folders acted on, in order
	Absorbed int      // flatten: subfolders folded in
}

// Apply runs one edit and journals it, synced before it returns (Seq is set
// here). A refused edit — a fixed folder, a merge of one — changes nothing and
// returns why. A journal that can't be written undoes the edit too: the next
// session would never hear of it.
func (d *Draft) Apply(e Edit) (Outcome, error) {
	tree, out, err := applyEdit(d.tree, e)
	if err != nil {
		return Outcome{}, err
	}
	e.Seq = len(d.edits) + 1
	if err := appendDraft(d.dir, e); err != nil {
		d.tree = replay(cloneTree(d.base), d.edits)
		return Outcome{}, err
	}
	sortTree(tree)
	d.tree, d.edits = tree, append(d.edits, e)
	return out, nil
}

// Undo drops the last edit from the journal and rebuilds the tree from base
// plus the edits left — the journal is the whole history, so there is no
// snapshot stack beside it. Returns the edit undone.
func (d *Draft) Undo() (Edit, error) {
	n := len(d.edits)
	if n == 0 {
		return Edit{}, ErrNothingToUndo
	}
	if err := writeDraft(d.dir, d.edits[:n-1]); err != nil {
		return Edit{}, err
	}
	last := d.edits[n-1]
	d.edits = d.edits[:n-1]
	d.tree = replay(cloneTree(d.base), d.edits)
	return last, nil
}

// Reset throws every edit away and goes back to the plan as proposed. The
// database is untouched — it never held the edits.
func (d *Draft) Reset() error {
	if err := RemoveDraft(d.dir); err != nil {
		return err
	}
	d.edits = nil
	d.tree = cloneTree(d.base)
	return nil
}

// applyEdit is the one place an Edit becomes a tree change. It validates
// before it touches the tree, so a refusal leaves tree as it was.
func applyEdit(tree []Node, e Edit) ([]Node, Outcome, error) {
	switch e.Op {
	case OpRename:
		n := FindNode(tree, e.Node)
		switch {
		case n == nil:
			return tree, Outcome{}, fmt.Errorf("internal error locating folder %d", e.Node)
		case n.Fixed():
			return tree, Outcome{}, ErrFixedFolder
		case e.To == "":
			return tree, Outcome{}, fmt.Errorf("a folder needs a name")
		}
		n.Name = e.To
		return tree, Outcome{Focus: n.ID, Name: e.To}, nil
	case OpMerge:
		t, id, name, parent, err := mergeNodes(tree, e.Nodes)
		return t, Outcome{Focus: id, Name: name, Parent: parent}, err
	case OpDrop:
		t, names, err := dropNodes(tree, e.Nodes)
		return t, Outcome{Names: names}, err
	case OpFlatten:
		t, absorbed, names, err := flattenNodes(tree, e.Nodes)
		return t, Outcome{Names: names, Absorbed: absorbed}, err
	}
	return tree, Outcome{}, fmt.Errorf("unknown review edit %q", e.Op)
}

// replay applies edits to tree, in order, and returns the result. Idempotent:
// an edit naming folders that no longer exist, or that no longer changes
// anything, does nothing. That is what makes a crash between ApplyDraft's
// commit and its RemoveDraft harmless — the applied tree comes back from the
// database with the merged-away and dropped folders gone and the renames
// already made, and the same journal replays onto it as a no-op.
func replay(tree []Node, edits []Edit) []Node {
	for _, e := range edits {
		// the anchor names a merge's result; without it this is a different merge
		if e.Op == OpMerge && (len(e.Nodes) == 0 || FindNode(tree, e.Nodes[0]) == nil) {
			continue
		}
		e.Nodes = present(tree, e.Nodes)
		if t, _, err := applyEdit(tree, e); err == nil {
			tree = t
		}
		sortTree(tree) // the review re-sorts after every edit; match its order
	}
	return tree
}

// present keeps the ids still in tree, in order.
func present(tree []Node, ids []int64) []int64 {
	var out []int64
	for _, id := range ids {
		if FindNode(tree, id) != nil {
			out = append(out, id)
		}
	}
	return out
}

// ApplyDraft writes the review's edits into the plan (spec D18): replays the
// draft over the stored tree, then Confirm applies it and approves every
// still-proposed row in one transaction, then the draft goes. Run before a
// transfer — review itself never writes the plan. A library with nothing left
// to review only drops the draft.
func ApplyDraft(ctx context.Context, database *db.DB, outputDir string) error {
	tree, err := BuildTree(ctx, database)
	if err != nil {
		return err
	}
	d, err := OpenDraft(outputDir, tree)
	if err != nil {
		return err
	}
	if len(tree) > 0 {
		if err := Confirm(ctx, database, d.Tree()); err != nil {
			return err
		}
	}
	return RemoveDraft(outputDir)
}
