// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package db_test

import (
	"context"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
)

func TestResetWipesAllTables(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)

	dbtest.SeedFile(t, d, 1, "/src", "photo.jpg", 1024)
	seed := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO file_metadata (file_hash, file_id) VALUES ('abc', 1)`, nil},
		{`INSERT INTO user_labels (label, kind) VALUES ('Goa Trip', 'EVENT')`, nil},
	}
	for _, s := range seed {
		if _, err := d.ExecContext(ctx, s.query, s.args...); err != nil {
			t.Fatalf("seed %q: %v", s.query, err)
		}
	}

	dbtest.SeedEntry(t, d, 1, "/src/photo.jpg", "2024/06_June/photo.jpg", db.StatusProposed)

	resp, err := d.ResetAll(ctx)
	if err != nil {
		t.Fatalf("ResetAll: %v", err)
	}

	counts := map[string]int64{
		"virtual_fs_entries": resp.VFSEntriesDeleted,
		"file_metadata":      resp.FileMetadataDeleted,
		"file_registry":      resp.FilesDeleted,
		"user_labels":        resp.UserLabelsDeleted,
	}
	for table, deleted := range counts {
		if deleted != 1 {
			t.Errorf("reported %d deleted rows for %s, want 1", deleted, table)
		}
		var remaining int
		if err := d.SQL.GetContext(ctx, &remaining, `SELECT count(*) FROM `+table); err != nil {
			t.Fatal(err)
		}
		if remaining != 0 {
			t.Errorf("%s has %d rows after reset, want 0", table, remaining)
		}
	}
	var folders int
	if err := d.SQL.GetContext(ctx, &folders, `SELECT count(*) FROM folder_nodes`); err != nil {
		t.Fatal(err)
	}
	if folders != 0 {
		t.Errorf("folder_nodes has %d rows after reset, want 0", folders)
	}
}

// TestResetKeepsLibrarySettings: a factory wipe of the *data* is not a
// request to forget which folders the user wants. The settings row is the
// library's own configuration, not something a scan produced.
func TestResetKeepsLibrarySettings(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)

	if _, err := d.ExecContext(ctx, `INSERT INTO library_settings
		(id, rules, collapse_levels, saved_places_date_only, merge_same_location_days, saved_places)
		VALUES (1, '["device"]', 1, 1, 1, '["Indore"]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ResetAll(ctx); err != nil {
		t.Fatalf("ResetAll: %v", err)
	}

	var rules string
	if err := d.SQL.GetContext(ctx, &rules, `SELECT rules FROM library_settings WHERE id = 1`); err != nil {
		t.Fatalf("the settings row must survive a reset: %v", err)
	}
	if rules != `["device"]` {
		t.Errorf("rules = %q after reset, want them untouched", rules)
	}

	// ...and an empty database is still empty with only settings in it, so
	// `reset --db` on a fresh library still refuses rather than replacing a
	// backup that holds real data.
	empty, err := d.IsEmpty(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !empty {
		t.Error("a library holding only its settings must still count as empty")
	}
}
