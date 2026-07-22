// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

// review.go is the shared reconcile core behind issue #8's two review surfaces
// (the HTTP API and the `wandersort review` CLI). It reads the PROPOSED rows the
// VFS build wrote and exposes them as a directory-only tree; it applies the
// user's edits back onto virtual_fs_entries and remembers renamed location
// names in user_labels so future scans suggest them automatically.
//
// Nodes are matched by an immutable ID (the proposed directory path at build
// time), never by diffing two trees — the client edits Name fields and returns
// the same tree, IDs untouched.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/db"
)

// ErrInvalidTree wraps every rejection of a submitted review tree (unknown node
// id, unsafe name, colliding rename). Callers at a trust boundary map it to a 4xx.
var ErrInvalidTree = errors.New("invalid review tree")

// ErrNoProposal means there is nothing to review: no scan has proposed
// anything yet, or a rescan replaced the proposal mid-review. Callers map it
// to a 404 (HTTP) or a "run scan first" hint (CLI).
var ErrNoProposal = errors.New("no proposal to review")

// Suggestion is a proposed name for a directory node, carried from the VFS
// build so the review UI can offer a one-key accept.
type Suggestion struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// Node is one directory in the proposed hierarchy. Both review surfaces
// serialize this tree. ID is the proposed dir path at build time and is
// immutable — reconcile matches on it. Name is the editable last segment.
type Node struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	FileCount   int          `json:"fileCount"`
	Samples     []string     `json:"samples,omitempty"`
	Suggestions []Suggestion `json:"suggestions,omitempty"`
	Children    []Node       `json:"children"`
}

const maxSamples = 3

// suggestionDepth is the dir-path segment (0-indexed, below Year=0/Month=1)
// that carries a file's location/event suggestion — the node a reviewer renames
// ("Unlocated" → "Manali"). Matches DefaultConfig's first configurable slot.
// ponytail: assumes location is the first slot; reorder Slots and the hint
// lands on the wrong node — the rename still reconciles, only the offered
// suggestion/label is misplaced. Thread Config in here if slot order goes live.
const suggestionDepth = 2

// ProposalSession returns the session that wrote the current proposal set
// (each VFS run replaces the set wholesale, so all rows share one session).
func ProposalSession(ctx context.Context, database *db.DB) (uuid.UUID, error) {
	var raw string
	err := database.SQL.GetContext(ctx, &raw,
		`SELECT session_id FROM virtual_fs_entries ORDER BY id DESC LIMIT 1`)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, ErrNoProposal
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("load proposal session: %w", err)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse proposal session id: %w", err)
	}
	return id, nil
}

// BuildTree reads the session's still-reviewable entries (PROPOSED/APPROVED —
// executed or failed rows are past reviewing) and returns the proposed
// directory tree (folder names only, no files). An empty result means the
// session has no proposal — the caller decides whether that is a 404.
func BuildTree(ctx context.Context, sessionID uuid.UUID, database *db.DB) ([]Node, error) {
	var rows []struct {
		TargetPath       string  `db:"target_path"`
		SourcePath       string  `db:"source_path"`
		Suggestion       *string `db:"suggestion"`
		SuggestionSource *string `db:"suggestion_source"`
	}
	if err := database.SQL.SelectContext(ctx, &rows,
		`SELECT target_path, source_path, suggestion, suggestion_source
		 FROM virtual_fs_entries WHERE session_id = ? AND status IN (?, ?)`,
		sessionID.String(), db.StatusProposed, db.StatusApproved); err != nil {
		return nil, fmt.Errorf("query vfs entries: %w", err)
	}

	type tnode struct {
		Node
		childIdx map[string]*tnode
		sugSeen  map[string]bool
	}
	newT := func(id, name string) *tnode {
		return &tnode{Node: Node{ID: id, Name: name}, childIdx: map[string]*tnode{}, sugSeen: map[string]bool{}}
	}
	root := newT("", "")

	for _, r := range rows {
		segs := strings.Split(path.Dir(r.TargetPath), "/")
		cur := root
		nodes := make([]*tnode, 0, len(segs))
		for i, seg := range segs {
			child, ok := cur.childIdx[seg]
			if !ok {
				child = newT(strings.Join(segs[:i+1], "/"), seg)
				cur.childIdx[seg] = child
			}
			cur = child
			nodes = append(nodes, cur)
			// counts and samples accumulate on every ancestor, so any node a
			// reviewer lands on can report its size and open a preview
			cur.FileCount++
			if len(cur.Samples) < maxSamples {
				cur.Samples = append(cur.Samples, r.SourcePath)
			}
		}
		// the location/event suggestion attaches to the node a reviewer
		// renames; paths too shallow to have one (fallback dir, custom slot
		// configs) get no suggestion rather than a misplaced one on Year/Month
		if len(nodes) <= suggestionDepth {
			continue
		}
		loc := nodes[suggestionDepth]
		if r.Suggestion != nil && *r.Suggestion != "" {
			src := ""
			if r.SuggestionSource != nil {
				src = *r.SuggestionSource
			}
			if key := *r.Suggestion + "\x00" + src; !loc.sugSeen[key] {
				loc.sugSeen[key] = true
				loc.Suggestions = append(loc.Suggestions, Suggestion{Name: *r.Suggestion, Source: src})
			}
		}
	}
	if len(root.childIdx) == 0 {
		return nil, nil
	}

	var finalize func(t *tnode) []Node
	finalize = func(t *tnode) []Node {
		names := make([]string, 0, len(t.childIdx))
		for n := range t.childIdx {
			names = append(names, n)
		}
		sort.Strings(names)
		out := make([]Node, 0, len(names))
		for _, n := range names {
			c := t.childIdx[n]
			node := c.Node
			node.Children = finalize(c)
			out = append(out, node)
		}
		return out
	}
	return finalize(root), nil
}

// Confirm applies the (possibly edited) tree back onto the session's entries:
// it rewrites target_path for every renamed directory, flips PROPOSED rows to
// APPROVED, and records renamed location nodes in user_labels so later scans
// suggest the corrected name. Nodes are matched by ID; unknown IDs, unsafe
// names, and renames that collide are rejected (trust boundary — the HTTP
// surface submits this). The write is synchronous: a nil return means the
// changes are committed.
func Confirm(ctx context.Context, sessionID uuid.UUID, database *db.DB, roots []Node) error {
	var targets []string
	if err := database.SQL.SelectContext(ctx, &targets,
		`SELECT DISTINCT target_path FROM virtual_fs_entries WHERE session_id = ?`, sessionID.String()); err != nil {
		return fmt.Errorf("load vfs dirs: %w", err)
	}
	if len(targets) == 0 {
		return fmt.Errorf("%w for session %s", ErrNoProposal, sessionID)
	}
	valid := map[string]bool{}
	for _, tp := range targets {
		parts := strings.Split(path.Dir(tp), "/")
		for i := range parts {
			valid[strings.Join(parts[:i+1], "/")] = true
		}
	}

	// walk the submitted tree: old-path (node ID) → new-path (edited ancestors)
	type labelWrite struct{ oldDir, name string }
	remap := map[string]string{}
	byNew := map[string]string{} // new-path → node ID, to reject colliding renames
	var labels []labelWrite
	var walk func(nodes []Node, parentNew string) error
	walk = func(nodes []Node, parentNew string) error {
		for _, n := range nodes {
			if !valid[n.ID] {
				return fmt.Errorf("%w: unknown node id %q", ErrInvalidTree, n.ID)
			}
			name := strings.TrimSpace(n.Name)
			if name == "" || name == "." || name == ".." {
				return fmt.Errorf("%w: invalid node name %q", ErrInvalidTree, n.Name)
			}
			name = sanitizeSegment(name)
			newPath := name
			if parentNew != "" {
				newPath = parentNew + "/" + name
			}
			// ponytail: collision check covers submitted nodes only; an API
			// client omitting part of the tree could still rename onto an
			// omitted sibling — full-tree submits (the CLI always) are covered
			if prev, dup := byNew[newPath]; dup {
				return fmt.Errorf("%w: %q and %q would both become %q", ErrInvalidTree, prev, n.ID, newPath)
			}
			byNew[newPath] = n.ID
			remap[n.ID] = newPath
			if newPath != n.ID && len(n.Suggestions) > 0 && !hasUserLabel(n.Suggestions) {
				labels = append(labels, labelWrite{oldDir: n.ID, name: name})
			}
			if err := walk(n.Children, newPath); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(roots, ""); err != nil {
		return err
	}

	// capture-time spans, only needed to date the new labels
	var capRows []capRow
	if len(labels) > 0 {
		if err := database.SQL.SelectContext(ctx, &capRows, `
			SELECT vfe.target_path, fm.exif_date_time_original, fm.exif_create_date, fr.file_modified_at
			FROM virtual_fs_entries vfe
			JOIN file_registry fr ON fr.id = vfe.file_id
			JOIN file_metadata fm ON fm.file_id = vfe.file_id
			WHERE vfe.session_id = ?`, sessionID.String()); err != nil {
			return fmt.Errorf("load capture times: %w", err)
		}
	}

	if err := database.Writer.WriteSync(func(ctx context.Context, tx *sqlx.Tx) error {
		// only still-reviewable rows are renamed; executed/failed rows keep
		// the paths they were moved under
		var entries []struct {
			ID         int64  `db:"id"`
			TargetPath string `db:"target_path"`
		}
		if err := tx.SelectContext(ctx, &entries,
			`SELECT id, target_path FROM virtual_fs_entries WHERE session_id = ? AND status IN (?, ?)`,
			sessionID.String(), db.StatusProposed, db.StatusApproved); err != nil {
			return err
		}
		// a rescan replaces the proposal set wholesale; if it won the race the
		// session's rows are gone and this confirm must fail, not half-apply
		if len(entries) == 0 {
			return fmt.Errorf("%w: proposal was replaced by a newer scan", ErrNoProposal)
		}
		for _, e := range entries {
			dir := path.Dir(e.TargetPath)
			if newDir, ok := remap[dir]; ok && newDir != dir {
				if _, err := tx.ExecContext(ctx,
					`UPDATE virtual_fs_entries SET target_path = ? WHERE id = ?`,
					newDir+"/"+path.Base(e.TargetPath), e.ID); err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE virtual_fs_entries SET status = ? WHERE session_id = ? AND status = ?`,
			db.StatusApproved, sessionID.String(), db.StatusProposed); err != nil {
			return err
		}
		for _, l := range labels {
			var ts, te any
			if start, end, ok := spanFor(capRows, l.oldDir); ok {
				ts, te = db.FormatTime(start), db.FormatTime(end)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO user_labels (label, kind, time_start, time_end)
				VALUES (?, 'EVENT', ?, ?)`, l.name, ts, te); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("confirm vfs: %w", err)
	}
	return nil
}

type capRow struct {
	TargetPath string  `db:"target_path"`
	DateOrig   *string `db:"exif_date_time_original"`
	CreateDate *string `db:"exif_create_date"`
	ModifiedAt string  `db:"file_modified_at"`
}

// spanFor returns the min/max capture time of every file under oldDir (that
// dir or any descendant), used to date a freshly written EVENT label.
func spanFor(rows []capRow, oldDir string) (start, end time.Time, ok bool) {
	for _, r := range rows {
		d := path.Dir(r.TargetPath)
		if d != oldDir && !strings.HasPrefix(d, oldDir+"/") {
			continue
		}
		t := firstTime(deref(r.DateOrig), deref(r.CreateDate), r.ModifiedAt)
		if t.IsZero() {
			continue
		}
		if !ok || t.Before(start) {
			start = t
		}
		if !ok || t.After(end) {
			end = t
		}
		ok = true
	}
	return start, end, ok
}

func hasUserLabel(ss []Suggestion) bool {
	for _, s := range ss {
		if s.Source == SuggestionUserLabel {
			return true
		}
	}
	return false
}
