// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package migrations

import (
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func swapSchemas(t *testing.T, replacement []Migration) {
	t.Helper()
	original := schemas
	schemas = replacement
	t.Cleanup(func() { schemas = original })
}

func testMigration(version uint) Migration {
	return Migration{
		Version:     version,
		Description: "test",
		SQL:         []string{`CREATE TABLE IF NOT EXISTS t (id INTEGER)`},
	}
}

func TestMigrations(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"RunAppliesOutOfOrderVersionsOnce", func(t *testing.T) {
			db := openTestDB(t)

			// A high version already applied must not block a lower one added later.
			swapSchemas(t, []Migration{testMigration(1000)})
			if n, err := Run(db); err != nil || n != 1 {
				t.Fatalf("first run: got (%d, %v), want (1, nil)", n, err)
			}

			swapSchemas(t, []Migration{testMigration(1000), testMigration(1)})
			if n, err := Run(db); err != nil || n != 1 {
				t.Fatalf("run with late lower version: got (%d, %v), want (1, nil)", n, err)
			}

			if n, err := Run(db); err != nil || n != 0 {
				t.Fatalf("rerun: got (%d, %v), want (0, nil)", n, err)
			}

			var versions []uint
			if err := db.Select(&versions, `SELECT version FROM schema_migrations ORDER BY version`); err != nil {
				t.Fatalf("select versions: %v", err)
			}
			if len(versions) != 2 || versions[0] != 1 || versions[1] != 1000 {
				t.Fatalf("applied versions = %v, want [1 1000]", versions)
			}
		}},
		{"RunRejectsDuplicateVersions", func(t *testing.T) {
			db := openTestDB(t)
			swapSchemas(t, []Migration{testMigration(3), testMigration(3)})
			if _, err := Run(db); err == nil {
				t.Fatal("want duplicate version error, got nil")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}
