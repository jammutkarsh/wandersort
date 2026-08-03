// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

// review.go is the reconcile core behind `wandersort review`: exposes PROPOSED
// rows as a directory tree, applies edits back onto virtual_fs_entries, and
// remembers the names the reviewer typed. Nodes match by immutable ID, never
// by tree diff.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	wspath "github.com/jammutkarsh/wandersort/pkg/path"
)

// ErrInvalidTree wraps every rejection of a submitted review tree (unknown node
// id, unsafe name, colliding rename).
var ErrInvalidTree = errors.New("invalid review tree")

// ErrNoProposal means there is nothing to review: no scan has proposed
// anything yet, or a rescan replaced the proposal mid-review. The CLI maps it
// to a "run scan first" hint.
var ErrNoProposal = errors.New("no proposal to review")

// Node is one directory in the proposed hierarchy, as the review TUI edits
// it. ID is the proposed dir path at build time and is
// immutable — reconcile matches on it. Name is the editable last segment.
type Node struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	FileCount int      `json:"fileCount"`
	Samples   []string `json:"samples,omitempty"`
	Children  []Node   `json:"children"`
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
		TargetPath  string   `db:"target_path"`
		SourcePath  string   `db:"source_path"`
		LocationDir *string  `db:"location_dir"`
		GPSLat      *float64 `db:"exif_gps_latitude"`
		GPSLon      *float64 `db:"exif_gps_longitude"`
	}
	if err := database.SQL.SelectContext(ctx, &rows,
		`SELECT vfe.target_path, vfe.source_path, vfe.location_dir,
		        fm.exif_gps_latitude, fm.exif_gps_longitude
		 FROM virtual_fs_entries vfe
		 LEFT JOIN file_metadata fm ON fm.file_id = vfe.file_id
		 WHERE vfe.status IN (?, ?)`,
		db.StatusProposed, db.StatusApproved); err != nil {
		return nil, fmt.Errorf("query vfs entries: %w", err)
	}

	type tnode struct {
		Node
		childIdx map[string]*tnode
	}
	newT := func(id, name string) *tnode {
		return &tnode{Node: Node{ID: id, Name: name}, childIdx: map[string]*tnode{}}
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
		// GPS attaches to the exact folder the location level emitted, not a
		// guessed depth — a guessed depth moved with Rules order and hung one
		// file's coordinates off whatever shared node sat there.
		if r.LocationDir == nil || *r.LocationDir == "" {
			continue
		}
		depth := strings.Count(*r.LocationDir, "/")
		if depth >= len(nodes) || nodes[depth].ID != *r.LocationDir {
			continue
		}
		if loc := nodes[depth]; loc.Lat == nil && r.GPSLat != nil && r.GPSLon != nil {
			loc.Lat, loc.Lon = r.GPSLat, r.GPSLon
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

// Labels returns every folder name the reviewer has typed in an earlier
// review, for this review's rename completions to offer back. The read side of
// what Confirm writes — one package understands the table, rather than the
// writer living here and the reader in the TUI.
//
// A failure is not one: it costs the reviewer their "used before" completions,
// never the review itself, so it warns and returns nothing.
func Labels(ctx context.Context, database *db.DB, log logger.Logger) []string {
	if database == nil {
		return nil
	}
	var labels []string
	if err := database.SQL.SelectContext(ctx, &labels,
		`SELECT DISTINCT label FROM user_labels ORDER BY label`); err != nil {
		if log != nil {
			log.Warn("Could not load confirmed labels for rename suggestions", "error", err)
		}
		return nil
	}
	return labels
}

// Confirm applies the (possibly edited) tree back onto the proposal's
// entries, flips PROPOSED rows to APPROVED, and remembers every name the
// reviewer typed in user_labels, so the next review's rename completions
// offer it. The write is synchronous: a nil return means committed.
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
	remap := map[string]string{}
	learned := map[string]bool{} // names the reviewer gave a folder, deduped
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
			// compare the *segment*, not the path: a merge moves a node under a
			// new parent without renaming it, and the name it kept is the
			// pipeline's own, not something worth completing later
			if name != path.Base(n.ID) {
				learned[name] = true
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
		for _, name := range slices.Sorted(maps.Keys(learned)) {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO user_labels (label, kind) VALUES (?, 'EVENT')`, name); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("confirm vfs: %w", err)
	}
	return nil
}
