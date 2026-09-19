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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/db"
)

// DraftFileName is the review's edit journal (spec D17), next to the database:
// one JSON line per edit, appended and synced as the reviewer makes it. The
// database only ever holds the plan as proposed; the draft is everything the
// reviewer did to it since, so a crash mid-review loses nothing and edits
// never bloat the database. ApplyDraft writes it into the plan.
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
	Node  int64   `json:"node,omitempty"`
	Nodes []int64 `json:"nodes,omitempty"`
	From  string  `json:"from,omitempty"`
	To    string  `json:"to,omitempty"`
}

func draftPath(outputDir string) string { return filepath.Join(outputDir, DraftFileName) }

// ReadDraft returns the journalled edits in order; no file is no edits. A
// torn last line (a crash mid-append) is dropped, and the file rewritten
// without it: that edit never finished being recorded, and leaving the
// fragment would glue the next append onto it — one broken line, the new
// edit lost, and the one after that an error every later read hits. A bad
// line anywhere else is an error — skipping it would replay the rest onto a
// tree it was never made against.
func ReadDraft(outputDir string) ([]Edit, error) {
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
				if err := WriteDraft(outputDir, edits); err != nil {
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
		if err := WriteDraft(outputDir, edits); err != nil {
			return nil, err
		}
	}
	return edits, nil
}

// AppendDraft records one edit and syncs it to disk before returning, so an
// edit on screen is an edit that survives a crash.
func AppendDraft(outputDir string, e Edit) error {
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode review edit: %w", err)
	}
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
	return nil
}

// WriteDraft replaces the whole journal with edits — [u] drops the last line
// this way. Written beside the file and renamed over it, so a crash leaves
// either the old journal or the new one. No edits removes the file.
func WriteDraft(outputDir string, edits []Edit) error {
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

// Replay applies edits to tree, in order, and returns the result. Idempotent:
// an edit naming folders that no longer exist, or that no longer changes
// anything, does nothing. That is what makes a crash between ApplyDraft's
// commit and its RemoveDraft harmless — the applied tree comes back from the
// database with the merged-away and dropped folders gone and the renames
// already made, and the same journal replays onto it as a no-op.
func Replay(tree []Node, edits []Edit) []Node {
	for _, e := range edits {
		tree = replayOne(tree, e)
		SortTree(tree) // the review re-sorts after every edit; match its order
	}
	return tree
}

func replayOne(tree []Node, e Edit) []Node {
	switch e.Op {
	case OpRename:
		if n := FindNode(tree, e.Node); n != nil && !n.Fixed() && e.To != "" {
			n.Name = e.To
		}
	case OpMerge:
		// the anchor names the result; without it this is a different merge
		if len(e.Nodes) == 0 || FindNode(tree, e.Nodes[0]) == nil {
			return tree
		}
		if ids := present(tree, e.Nodes); len(ids) >= 2 {
			if t, _, _, _, err := MergeNodes(tree, ids); err == nil {
				return t
			}
		}
	case OpDrop:
		if ids := present(tree, e.Nodes); len(ids) > 0 {
			if t, _, err := DropNodes(tree, ids); err == nil {
				return t
			}
		}
	case OpFlatten:
		if ids := present(tree, e.Nodes); len(ids) > 0 {
			if t, _, _, err := FlattenNodes(tree, ids); err == nil {
				return t
			}
		}
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
	edits, err := ReadDraft(outputDir)
	if err != nil {
		return err
	}
	tree, err := BuildTree(ctx, database)
	if err != nil {
		return err
	}
	if len(tree) > 0 {
		if err := Confirm(ctx, database, Replay(tree, edits)); err != nil {
			return err
		}
	}
	return RemoveDraft(outputDir)
}
