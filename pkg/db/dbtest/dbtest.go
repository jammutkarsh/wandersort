// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dbtest provides shared database fixtures for tests: a fresh
// migrated app database and seed helpers for the tables every pipeline
// phase touches
package dbtest

import (
	"context"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// New opens a fresh migrated app database under t.TempDir()
func New(t testing.TB) *db.DB {
	t.Helper()
	d, err := db.New(context.Background(), filepath.Join(t.TempDir(), "test.db"), logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// SeedFile inserts a live file_registry row with fixed timestamps and returns nothing;
// callers pick the id so tests can reference rows without querying back
func SeedFile(t testing.TB, d *db.DB, id int64, dir, name string, size int64) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO file_registry (id, file_dir, file_name, file_size, file_modified_at,
			file_extension, media_type, discovered_at, last_seen_at)
		VALUES (?, ?, ?, ?, '2024-01-01T00:00:00.000000000Z', ?, 'IMAGE',
			'2024-01-01T00:00:00.000000000Z', '2024-01-01T00:00:00.000000000Z')`,
		id, dir, name, size, filepath.Ext(name)); err != nil {
		t.Fatal(err)
	}
}

// SeedHash gives fileID the file_metadata row a scan would have written,
// holding only its hash — what execute verifies a copy against.
func SeedHash(t testing.TB, d *db.DB, fileID int64, hash string) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO file_metadata (file_hash, file_id) VALUES (?, ?)`, hash, fileID); err != nil {
		t.Fatal(err)
	}
}

// SeedEntry inserts a pending virtual_fs_entries row for fileID, creating (or
// reusing) the folder_nodes chain its target folder needs, and returns the
// row's node_id. Placing or failing the file is a separate act (SeedPlaced,
// SeedTransferError). Levels are left blank: a test that cares about them goes through
// the planner instead.
func SeedEntry(t testing.TB, d *db.DB, fileID int64, source, target string) int64 {
	t.Helper()
	ctx := context.Background()
	var parent any // nil = top level
	for _, name := range strings.Split(path.Dir(target), "/") {
		var id int64
		err := d.QueryRowContext(ctx,
			`SELECT id FROM folder_nodes WHERE parent_id IS ? AND name = ?`, parent, name).Scan(&id)
		if err != nil {
			res, err := d.ExecContext(ctx,
				`INSERT INTO folder_nodes (parent_id, name, level) VALUES (?, ?, '')`, parent, name)
			if err != nil {
				t.Fatal(err)
			}
			if id, err = res.LastInsertId(); err != nil {
				t.Fatal(err)
			}
		}
		parent = id
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO virtual_fs_entries (file_id, source_path, node_id, target_path)
		VALUES (?, ?, ?, ?)`, fileID, source, parent, target); err != nil {
		t.Fatal(err)
	}
	return parent.(int64)
}

// SeedPlaced marks fileID as landed in the library.
func SeedPlaced(t testing.TB, d *db.DB, fileID int64) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(),
		`UPDATE file_registry SET placed = 1 WHERE id = ?`, fileID); err != nil {
		t.Fatal(err)
	}
}

// SeedTransferError gives fileID the TRANSFER failure execute would record.
func SeedTransferError(t testing.TB, d *db.DB, fileID int64, message string) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO errors (file_id, stage, op, kind, detail, first_seen_at, last_seen_at)
		VALUES (?, ?, 'copy', 'other', ?, '2024-01-01T00:00:00.000000000Z', '2024-01-01T00:00:00.000000000Z')`,
		fileID, db.StageTransfer, `{"message":"`+message+`"}`); err != nil {
		t.Fatal(err)
	}
}
