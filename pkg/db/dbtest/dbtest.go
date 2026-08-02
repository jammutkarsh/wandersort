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
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// New opens a fresh migrated app database under t.TempDir()
func New(t testing.TB) *db.DB {
	t.Helper()
	d, err := db.New(context.Background(), filepath.Join(t.TempDir(), "test.db"), db.AppDB, logger.NewNoopLogger())
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
