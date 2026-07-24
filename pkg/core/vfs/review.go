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
	// Lat/Lon are one exemplar GPS coordinate for this node (only ever set on
	// the location-depth node, from the first GPS-tagged file that landed
	// there) — lets a review surface re-query the location resolver for ranked
	// alternatives when the reviewer wants to correct a wrong name.
	Lat *float64 `json:"lat,omitempty"`
	Lon *float64 `json:"lon,omitempty"`
	// MergedIDs are the IDs of nodes a review-time merge folded into this one.
	// They no longer appear anywhere in the tree (the reviewer sees one folder,
	// which is the whole point of merging), but their files still live under
	// those old paths in the DB, so Confirm must remap them here too.
	MergedIDs []string `json:"mergedIds,omitempty"`
}

const maxSamples = 3

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
		TargetPath       string   `db:"target_path"`
		SourcePath       string   `db:"source_path"`
		Suggestion       *string  `db:"suggestion"`
		SuggestionSource *string  `db:"suggestion_source"`
		SuggestionDir    *string  `db:"suggestion_dir"`
		GPSLat           *float64 `db:"exif_gps_latitude"`
		GPSLon           *float64 `db:"exif_gps_longitude"`
	}
	if err := database.SQL.SelectContext(ctx, &rows,
		`SELECT vfe.target_path, vfe.source_path, vfe.suggestion, vfe.suggestion_source,
		        vfe.suggestion_dir, fm.exif_gps_latitude, fm.exif_gps_longitude
		 FROM virtual_fs_entries vfe
		 LEFT JOIN file_metadata fm ON fm.file_id = vfe.file_id
		 WHERE vfe.session_id = ? AND vfe.status IN (?, ?)`,
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
		// the location/event suggestion attaches to the exact folder the VFS
		// build recorded it against — not a guessed depth, which moved with the
		// Rules order and smeared every file's suggestion onto one shared
		// Device/Day node. No suggestion_dir (no location level in this
		// proposal, or rows written before the column existed) means no
		// suggestion node, rather than a misplaced one.
		if r.SuggestionDir == nil || *r.SuggestionDir == "" {
			continue
		}
		depth := strings.Count(*r.SuggestionDir, "/")
		if depth >= len(nodes) || nodes[depth].ID != *r.SuggestionDir {
			continue
		}
		loc := nodes[depth]
		if loc.Lat == nil && r.GPSLat != nil && r.GPSLon != nil {
			loc.Lat, loc.Lon = r.GPSLat, r.GPSLon
		}
		// a suggestion identical to the folder's current name is noise — it
		// offers the reviewer the name they're already looking at (a source
		// folder named after the camera, say, next to a folder of the same
		// name)
		if r.Suggestion != nil && *r.Suggestion != "" && *r.Suggestion != loc.Name {
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

// FilesUnder returns the source paths of every file proposed under nodeID
// (that directory or any descendant), in a stable order — used by the review
// TUI's preview to stage more than the handful of Samples a Node carries.
func FilesUnder(ctx context.Context, sessionID uuid.UUID, nodeID string, database *db.DB) ([]string, error) {
	var paths []string
	// prefix compare, not GLOB/LIKE: a folder name can legitimately contain
	// *, ?, [ or ], and a pattern match would read those as wildcards
	prefix := nodeID + "/"
	if err := database.SQL.SelectContext(ctx, &paths,
		`SELECT source_path FROM virtual_fs_entries
		 WHERE session_id = ? AND status IN (?, ?) AND substr(target_path, 1, length(?)) = ?
		 ORDER BY source_path`,
		sessionID.String(), db.StatusProposed, db.StatusApproved, prefix, prefix); err != nil {
		return nil, fmt.Errorf("query files under %q: %w", nodeID, err)
	}
	return paths, nil
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

	// walk the submitted tree: old-path (node ID) → new-path (edited ancestors).
	// Two nodes renamed to the same path is a deliberate merge (e.g. two
	// unresolved date clusters turning out to be the same place), not an
	// error — remap tolerates many old IDs collapsing onto one new path, and
	// the per-file UPDATE loop below doesn't care how many did.
	type labelWrite struct {
		oldDirs []string // every merged node contributing to this label, so spanFor covers all of them
		name    string
	}
	remap := map[string]string{}
	// merged nodes are gone from the submitted tree along with everything under
	// them, so their descendants' dirs have no remap entry of their own — they
	// get rewritten by prefix instead (old subtree root -> new path)
	merged := map[string]string{}
	labelIdx := map[string]int{} // name+kind -> index into labels, so a merge accumulates oldDirs instead of double-inserting
	var labels []labelWrite
	var walk func(nodes []Node, parentNew string) error
	walk = func(nodes []Node, parentNew string) error {
		for _, n := range nodes {
			name := strings.TrimSpace(n.Name)
			if name == "" || name == "." || name == ".." {
				return fmt.Errorf("%w: invalid node name %q", ErrInvalidTree, n.Name)
			}
			name = sanitizeSegment(name)
			newPath := name
			if parentNew != "" {
				newPath = parentNew + "/" + name
			}
			oldDirs := append([]string{n.ID}, n.MergedIDs...)
			for _, id := range oldDirs {
				if !valid[id] {
					return fmt.Errorf("%w: unknown node id %q", ErrInvalidTree, id)
				}
				remap[id] = newPath
			}
			for _, id := range n.MergedIDs {
				merged[id] = newPath
			}
			if newPath != n.ID && len(n.Suggestions) > 0 && !hasUserLabel(n.Suggestions) {
				key := name + "\x00EVENT"
				if i, ok := labelIdx[key]; ok {
					labels[i].oldDirs = append(labels[i].oldDirs, oldDirs...)
				} else {
					labelIdx[key] = len(labels)
					labels = append(labels, labelWrite{oldDirs: oldDirs, name: name})
				}
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
		// Collapsing several dirs onto one can land two different files on the
		// same basename (distinct masters, reused camera counter). buildTargets'
		// own uniqueness guarantee only held for the layout it generated, so
		// re-establish it here: rows that aren't moving claim their path first,
		// then moved rows take the next free _N suffix.
		taken := map[string]bool{}
		type move struct {
			id      int64
			dir     string
			base    string
			oldPath string
		}
		var moves []move
		for _, e := range entries {
			dir := path.Dir(e.TargetPath)
			newDir, ok := remap[dir]
			if !ok {
				newDir, ok = remapUnderMerged(merged, dir)
			}
			if !ok || newDir == dir {
				taken[strings.ToLower(e.TargetPath)] = true
				continue
			}
			moves = append(moves, move{e.ID, newDir, path.Base(e.TargetPath), e.TargetPath})
		}
		for _, mv := range moves {
			ext := path.Ext(mv.base)
			stem := strings.TrimSuffix(mv.base, ext)
			var newPath string
			for n := 1; ; n++ {
				suffix := ""
				if n > 1 {
					suffix = fmt.Sprintf("_%d", n)
				}
				newPath = mv.dir + "/" + stem + suffix + ext
				if !taken[strings.ToLower(newPath)] {
					taken[strings.ToLower(newPath)] = true
					break
				}
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE virtual_fs_entries SET target_path = ? WHERE id = ?`,
				newPath, mv.id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE virtual_fs_entries SET status = ? WHERE session_id = ? AND status = ?`,
			db.StatusApproved, sessionID.String(), db.StatusProposed); err != nil {
			return err
		}
		for _, l := range labels {
			var ts, te any
			if start, end, ok := spanFor(capRows, l.oldDirs); ok {
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

// remapUnderMerged rewrites a dir that sits *below* a node folded away by a
// merge: the subtree root moves to the survivor's path and everything under it
// keeps its relative shape. Merges from the review TUI only ever fold leaf
// dirs (nothing below them), but the HTTP surface can submit MergedIDs on any
// node, and files under it must not be left pointing at the old path.
func remapUnderMerged(merged map[string]string, dir string) (string, bool) {
	for oldRoot, newRoot := range merged {
		if strings.HasPrefix(dir, oldRoot+"/") {
			return newRoot + dir[len(oldRoot):], true
		}
	}
	return "", false
}

type capRow struct {
	TargetPath string  `db:"target_path"`
	DateOrig   *string `db:"exif_date_time_original"`
	CreateDate *string `db:"exif_create_date"`
	ModifiedAt string  `db:"file_modified_at"`
}

// spanFor returns the min/max capture time of every file under any of oldDirs
// (each dir or its descendants), used to date a freshly written EVENT label —
// a merge contributes more than one oldDir, so the label spans all of them.
func spanFor(rows []capRow, oldDirs []string) (start, end time.Time, ok bool) {
	under := func(d string) bool {
		for _, oldDir := range oldDirs {
			if d == oldDir || strings.HasPrefix(d, oldDir+"/") {
				return true
			}
		}
		return false
	}
	for _, r := range rows {
		d := path.Dir(r.TargetPath)
		if !under(d) {
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
