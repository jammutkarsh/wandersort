// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"context"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
)

func TestResetWipesAllTables(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)

	sessionID := dbtest.NewSession(t, d, db.StatusCompleted)
	dbtest.SeedFile(t, d, sessionID, 1, "/src", "photo.jpg", 1024)
	seed := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO file_metadata (file_hash, file_id) VALUES ('abc', 1)`, nil},
		{`INSERT INTO virtual_fs_entries (session_id, file_id, source_path, target_path)
			VALUES (?, 1, '/src/photo.jpg', '2024/06_June/photo.jpg')`, []any{sessionID.String()}},
		{`INSERT INTO user_labels (label, kind) VALUES ('Goa Trip', 'EVENT')`, nil},
	}
	for _, s := range seed {
		if _, err := d.ExecContext(ctx, s.query, s.args...); err != nil {
			t.Fatalf("seed %q: %v", s.query, err)
		}
	}

	resp, err := NewRepository(d).Reset(ctx)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}

	counts := map[string]int64{
		"virtual_fs_entries": resp.VFSEntriesDeleted,
		"file_metadata":      resp.FileMetadataDeleted,
		"file_registry":      resp.FilesDeleted,
		"scan_sessions":      resp.ScanSessionsDeleted,
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
}
