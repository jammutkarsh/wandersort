// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package migrations

import (
	"errors"
	"testing"
	"time"

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
		{"RunRefusesVersionsItDoesNotKnow", func(t *testing.T) {
			db := openTestDB(t)
			swapSchemas(t, []Migration{testMigration(1), testMigration(2)})
			if _, err := Run(db); err != nil {
				t.Fatal(err)
			}
			// an older build: knows only version 1
			swapSchemas(t, []Migration{testMigration(1)})
			if _, err := Run(db); !errors.Is(err, ErrNewerSchema) {
				t.Fatalf("Run = %v, want ErrNewerSchema", err)
			}
			if _, _, err := Pending(db); !errors.Is(err, ErrNewerSchema) {
				t.Fatalf("Pending = %v, want ErrNewerSchema", err)
			}
		}},
		{"PendingCountsWhatRunWouldApply", func(t *testing.T) {
			db := openTestDB(t)
			swapSchemas(t, []Migration{testMigration(1)})
			if p, a, err := Pending(db); err != nil || p != 1 || a != 0 {
				t.Fatalf("fresh: Pending = (%d, %d, %v), want (1, 0, nil)", p, a, err)
			}
			if _, err := Run(db); err != nil {
				t.Fatal(err)
			}
			swapSchemas(t, []Migration{testMigration(1), testMigration(2)})
			if p, a, err := Pending(db); err != nil || p != 1 || a != 1 {
				t.Fatalf("upgrade: Pending = (%d, %d, %v), want (1, 1, nil)", p, a, err)
			}
		}},
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

// TestRunRecordsRunAtInTheStoredTimeForm keeps schema_migrations.run_at in
// the fixed-width UTC form every other stored timestamp uses.
func TestRunRecordsRunAtInTheStoredTimeForm(t *testing.T) {
	db := openTestDB(t)
	swapSchemas(t, []Migration{testMigration(1)})
	if _, err := Run(db); err != nil {
		t.Fatal(err)
	}
	var runAt string
	if err := db.Get(&runAt, `SELECT run_at FROM schema_migrations WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000000000Z07:00", runAt); err != nil {
		t.Errorf("run_at = %q, not the fixed-width stored form: %v", runAt, err)
	}
}
