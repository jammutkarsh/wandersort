// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

// folders.go is the plan's persisted folder tree (folder_nodes, spec D12): a
// folder keeps its id through renames and moves, and a file's folder path is
// its folder's ancestors' names joined.

import (
	"context"
	"database/sql/driver"
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/db"
)

// folderRow is one folder_nodes row. Parent 0 is the top level.
type folderRow struct {
	ID     int64  `db:"id"`
	Parent int64  `db:"parent_id"`
	Name   string `db:"name"`
	Level  string `db:"level"`
	Bounds Bounds `db:"bounds"`
}

// Scan reads a folder_nodes.bounds column.
func (b *Bounds) Scan(src any) error {
	*b = Bounds{}
	switch v := src.(type) {
	case string:
		return json.Unmarshal([]byte(v), b)
	case []byte:
		return json.Unmarshal(v, b)
	}
	return fmt.Errorf("folder bounds: unexpected %T", src)
}

// Value writes a folder_nodes.bounds column. No alternatives is "[]", never
// "null" — json/v2 writes a nil slice as [].
func (b Bounds) Value() (driver.Value, error) {
	out, err := json.Marshal(b)
	return string(out), err
}

func loadFolderRows(ctx context.Context, q sqlx.QueryerContext) (map[int64]folderRow, error) {
	var rows []folderRow
	if err := sqlx.SelectContext(ctx, q, &rows,
		`SELECT id, COALESCE(parent_id, 0) AS parent_id, name, level, bounds FROM folder_nodes`); err != nil {
		return nil, fmt.Errorf("load folders: %w", err)
	}
	out := make(map[int64]folderRow, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// folderPath is the "/"-joined path of folder id, memoised in paths.
func folderPath(rows map[int64]folderRow, paths map[int64]string, id int64) string {
	if p, ok := paths[id]; ok {
		return p
	}
	r := rows[id]
	p := r.Name
	if r.Parent != 0 {
		p = folderPath(rows, paths, r.Parent) + "/" + p
	}
	paths[id] = p
	return p
}

// folderKey names a folder by where it sits.
type folderKey struct {
	parent int64
	name   string
}

// folderIndex finds or creates the folder chain a planned directory needs.
type folderIndex struct {
	byKey  map[folderKey]int64
	chains map[string][]int64
}

// loadFolders indexes the folders a new proposal may reuse, so a folder that
// is planned again keeps its id. Folders holding a placed file, or above one,
// are left out: a review rename of a folder the proposal shares would also
// rename where the placed file is recorded, and placed files never move.
//
// ponytail: a new file for a placed folder gets a same-named twin folder
// instead of joining it. Issue 14 (placing new files through the tree) is
// where they should share.
func loadFolders(ctx context.Context, tx *sqlx.Tx) (*folderIndex, error) {
	var rows []folderRow
	if err := tx.SelectContext(ctx, &rows, placedFoldersCTE+`
		SELECT id, COALESCE(parent_id, 0) AS parent_id, name, level, bounds FROM folder_nodes
		WHERE id NOT IN (SELECT id FROM placed_folders)
		ORDER BY id`); err != nil {
		return nil, fmt.Errorf("load reusable folders: %w", err)
	}
	f := &folderIndex{byKey: make(map[folderKey]int64, len(rows)), chains: map[string][]int64{}}
	for _, r := range rows {
		k := folderKey{r.Parent, r.Name}
		if _, ok := f.byKey[k]; !ok {
			f.byKey[k] = r.ID
		}
	}
	return f, nil
}

// placedFoldersCTE names every folder holding a placed file, or above one, as
// placed_folders.
const placedFoldersCTE = `
	WITH RECURSIVE placed_folders(id) AS (
		SELECT vfe.node_id FROM virtual_fs_entries vfe
		JOIN file_registry fr ON fr.id = vfe.file_id
		WHERE fr.placed = 1
		UNION
		SELECT fn.parent_id FROM folder_nodes fn
		JOIN placed_folders pf ON fn.id = pf.id
		WHERE fn.parent_id IS NOT NULL
	)`

// splitPlacedFolders moves every still-reviewable entry whose folder chain
// passes through a folder holding a placed file (or above one) onto a
// same-path chain of its own, and returns old id → new id for every folder it
// split. A copy stopped partway leaves approved files beside or under the
// copied ones; without the split, a review rename of a shared folder would
// rename where the copied files are recorded, while on disk they stay put.
// The same rule loadFolders applies to a new plan.
func splitPlacedFolders(ctx context.Context, tx *sqlx.Tx) (map[int64]int64, error) {
	var entries []struct {
		ID           int64  `db:"id"`
		NodeID       int64  `db:"node_id"`
		LocationNode *int64 `db:"location_node_id"`
	}
	// placed_folders is closed upwards, so everything below it is every
	// folder whose chain touches it
	if err := tx.SelectContext(ctx, &entries, placedFoldersCTE+`,
		under_placed(id) AS (
			SELECT id FROM placed_folders
			UNION
			SELECT fn.id FROM folder_nodes fn JOIN under_placed u ON fn.parent_id = u.id
		)
		SELECT id, node_id, location_node_id FROM virtual_fs_entries
		WHERE status IN (?, ?) AND node_id IN (SELECT id FROM under_placed)
		ORDER BY id`, db.StatusProposed, db.StatusApproved); err != nil {
		return nil, fmt.Errorf("find reviewable files in placed folders: %w", err)
	}
	if len(entries) == 0 {
		return nil, nil
	}
	rows, err := loadFolderRows(ctx, tx)
	if err != nil {
		return nil, err
	}
	index, err := loadFolders(ctx, tx)
	if err != nil {
		return nil, err
	}
	twins := map[int64]int64{}
	paths := map[int64]string{}
	for _, e := range entries {
		if _, done := twins[e.NodeID]; !done {
			// the old chain, top first, and each folder's level
			var old []int64
			for id := e.NodeID; id != 0; id = rows[id].Parent {
				old = append([]int64{id}, old...)
			}
			levels := make([]string, len(old))
			for i, id := range old {
				levels[i] = rows[id].Level
			}
			chain, err := index.ensure(ctx, tx, folderPath(rows, paths, e.NodeID), levels)
			if err != nil {
				return nil, err
			}
			for i, id := range old {
				twins[id] = chain[i]
				// same path, same files: the twin means what the old folder did
				if _, err := tx.ExecContext(ctx, `UPDATE folder_nodes SET bounds = ? WHERE id = ?`,
					rows[id].Bounds, chain[i]); err != nil {
					return nil, fmt.Errorf("copy folder bounds: %w", err)
				}
			}
		}
		loc := e.LocationNode
		if loc != nil {
			if twin, ok := twins[*loc]; ok {
				loc = &twin
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE virtual_fs_entries SET node_id = ?, location_node_id = ? WHERE id = ?`,
			twins[e.NodeID], loc, e.ID); err != nil {
			return nil, fmt.Errorf("move file off a placed folder: %w", err)
		}
	}
	return twins, nil
}

// ensure returns the folder id of every segment of dir, top first, creating
// the ones that don't exist yet. levels names the level of each segment.
func (f *folderIndex) ensure(ctx context.Context, tx *sqlx.Tx, dir string, levels []string) ([]int64, error) {
	if c, ok := f.chains[dir]; ok {
		return c, nil
	}
	segs := strings.Split(dir, "/")
	chain := make([]int64, len(segs))
	var parent int64
	for i, name := range segs {
		k := folderKey{parent, name}
		id, ok := f.byKey[k]
		if !ok {
			level := ""
			if i < len(levels) {
				level = levels[i]
			}
			res, err := tx.ExecContext(ctx,
				`INSERT INTO folder_nodes (parent_id, name, level) VALUES (?, ?, ?)`,
				nullableID(parent), name, level)
			if err != nil {
				return nil, fmt.Errorf("create folder %q: %w", name, err)
			}
			if id, err = res.LastInsertId(); err != nil {
				return nil, fmt.Errorf("create folder %q: %w", name, err)
			}
			f.byKey[k] = id
		}
		chain[i] = id
		parent = id
	}
	f.chains[dir] = chain
	return chain, nil
}

// pruneFolders deletes every folder no entry sits in, directly or below. A
// folder with a file in it is never deleted, so a placed file's folder stays.
func pruneFolders(ctx context.Context, tx *sqlx.Tx) error {
	if _, err := tx.ExecContext(ctx, usedFoldersCTE+`
		DELETE FROM folder_nodes WHERE id NOT IN (SELECT id FROM used)`); err != nil {
		return fmt.Errorf("delete unused folders: %w", err)
	}
	return nil
}

// usedFoldersCTE names every folder an entry sits in, or above one, as used.
const usedFoldersCTE = `
	WITH RECURSIVE used(id) AS (
		SELECT node_id FROM virtual_fs_entries
		UNION
		SELECT fn.parent_id FROM folder_nodes fn
		JOIN used u ON fn.id = u.id
		WHERE fn.parent_id IS NOT NULL
	)`
