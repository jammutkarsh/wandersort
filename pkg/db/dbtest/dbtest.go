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

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// New opens a fresh migrated app database under t.TempDir()
func New(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.New(context.Background(), filepath.Join(t.TempDir(), "test.db"), db.AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// NewSession inserts a scan_sessions row with the given status and returns its id
func NewSession(t *testing.T, d *db.DB, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO scan_sessions (id, status, root_paths) VALUES (?, ?, '/tmp')`,
		id.String(), status); err != nil {
		t.Fatal(err)
	}
	return id
}

// SeedFile inserts a live file_registry row with fixed timestamps and returns nothing;
// callers pick the id so tests can reference rows without querying back
func SeedFile(t *testing.T, d *db.DB, sessionID uuid.UUID, id int64, dir, name string, size int64) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO file_registry (id, file_dir, file_name, file_size, file_modified_at,
			scan_session_id, file_extension, media_type, discovered_at, last_seen_at)
		VALUES (?, ?, ?, ?, '2024-01-01T00:00:00.000000000Z', ?, ?, 'IMAGE',
			'2024-01-01T00:00:00.000000000Z', '2024-01-01T00:00:00.000000000Z')`,
		id, dir, name, size, sessionID.String(), filepath.Ext(name)); err != nil {
		t.Fatal(err)
	}
}
