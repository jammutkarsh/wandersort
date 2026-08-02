// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

// review.go is the reconcile core behind `wandersort review`: exposes PROPOSED
// rows as a directory tree, applies edits back onto virtual_fs_entries, and
// records renamed locations. Nodes match by immutable ID, never by tree diff.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/db"
	wspath "github.com/jammutkarsh/wandersort/pkg/path"
)

// ErrInvalidTree wraps every rejection of a submitted review tree (unknown node
// id, unsafe name, colliding rename).
var ErrInvalidTree = errors.New("invalid review tree")

// ErrNoProposal means there is nothing to review: no scan has proposed
// anything yet, or a rescan replaced the proposal mid-review. The CLI maps it
// to a "run scan first" hint.
var ErrNoProposal = errors.New("no proposal to review")

// Suggestion is a proposed name for a directory node, carried from the VFS
// build so the review UI can offer a one-key accept.
type Suggestion struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// Node is one directory in the proposed hierarchy, as the review TUI edits
// it. ID is the proposed dir path at build time and is
// immutable — reconcile matches on it. Name is the editable last segment.
type Node struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	FileCount   int          `json:"fileCount"`
	Samples     []string     `json:"samples,omitempty"`
	Suggestions []Suggestion `json:"suggestions,omitempty"`
	Children    []Node       `json:"children"`
	// One exemplar GPS coordinate (location-depth node only), so the review UI
	// can re-query the resolver for ranked rename alternatives.
	Lat *float64 `json:"lat,omitempty"`
	Lon *float64 `json:"lon,omitempty"`
	// IDs of nodes a review-time merge folded into this one — gone from the
	// tree, but their files still live under those old paths, so Confirm
	// must remap them here too.
	MergedIDs []string `json:"mergedIds,omitempty"`
}

const maxSamples = 3

// BuildTree reads the still-reviewable entries (executed/failed rows are past
// reviewing) and returns the proposed directory tree, folders only. An empty
// result means no proposal exists — the caller decides if that's a 404.
func BuildTree(ctx context.Context, database *db.DB) ([]Node, error) {
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
		 WHERE vfe.status IN (?, ?)`,
		db.StatusProposed, db.StatusApproved); err != nil {
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
		// The suggestion attaches to the exact folder the VFS build recorded it
		// against, not a guessed depth — a guessed depth moved with Rules order
		// and smeared suggestions onto the wrong shared node.
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
		// identical-to-current-name is noise: it offers the reviewer the name
		// they're already looking at
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
func FilesUnder(ctx context.Context, nodeID string, database *db.DB) ([]string, error) {
	var paths []string
	// prefix compare, not GLOB/LIKE: a folder name can legitimately contain
	// *, ?, [ or ], and a pattern match would read those as wildcards
	prefix := nodeID + "/"
	if err := database.SQL.SelectContext(ctx, &paths,
		`SELECT source_path FROM virtual_fs_entries
		 WHERE status IN (?, ?) AND substr(target_path, 1, length(?)) = ?
		 ORDER BY source_path`,
		db.StatusProposed, db.StatusApproved, prefix, prefix); err != nil {
		return nil, fmt.Errorf("query files under %q: %w", nodeID, err)
	}
	return paths, nil
}

// Confirm applies the (possibly edited) tree back onto the proposal's
// entries, flips PROPOSED rows to APPROVED, and records renamed location
// nodes in user_labels. The write is synchronous: a nil return means committed.
func Confirm(ctx context.Context, database *db.DB, roots []Node) error {
	var targets []string
	if err := database.SQL.SelectContext(ctx, &targets,
		`SELECT DISTINCT target_path FROM virtual_fs_entries`); err != nil {
		return fmt.Errorf("load vfs dirs: %w", err)
	}
	if len(targets) == 0 {
		return ErrNoProposal
	}
	valid := map[string]bool{}
	for _, tp := range targets {
		parts := strings.Split(path.Dir(tp), "/")
		for i := range parts {
			valid[strings.Join(parts[:i+1], "/")] = true
		}
	}

	// old-path (node ID) → new-path. Two nodes renamed to the same path is a
	// deliberate merge, not an error — remap tolerates many old IDs
	// collapsing onto one new path.
	type labelWrite struct {
		oldDirs []string // every merged node contributing to this label, so spanFor covers all of them
		name    string
	}
	remap := map[string]string{}
	labelIdx := map[string]int{} // name+kind -> index into labels, so a merge accumulates oldDirs instead of double-inserting
	var labels []labelWrite
	var walk func(nodes []Node, parentNew string) error
	walk = func(nodes []Node, parentNew string) error {
		for _, n := range nodes {
			name := strings.TrimSpace(n.Name)
			if name == "" || name == "." || name == ".." {
				return fmt.Errorf("%w: invalid node name %q", ErrInvalidTree, n.Name)
			}
			name = wspath.SanitizeSegment(name)
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
			JOIN file_metadata fm ON fm.file_id = vfe.file_id`); err != nil {
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
			`SELECT id, target_path FROM virtual_fs_entries WHERE status IN (?, ?)`,
			db.StatusProposed, db.StatusApproved); err != nil {
			return err
		}
		// a rescan replaces the proposal set wholesale; if it won the race the
		// rows are gone and this confirm must fail, not half-apply
		if len(entries) == 0 {
			return fmt.Errorf("%w: proposal was replaced by a newer scan", ErrNoProposal)
		}
		// Collapsing dirs can land two files on the same basename; buildTargets'
		// uniqueness guarantee only held for its own layout, so re-establish it:
		// unmoved rows claim their path first, moved rows take the next _N.
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
			`UPDATE virtual_fs_entries SET status = ? WHERE status = ?`,
			db.StatusApproved, db.StatusProposed); err != nil {
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
