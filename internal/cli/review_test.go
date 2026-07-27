// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
)

// approvedCount is what --rebuild consults before throwing a plan away, so it
// stayed in cli when the review TUI moved to internal/review.
func TestApprovedCountGuardsRebuild(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)

	insert := func(fileID int64, name, status string) {
		dbtest.SeedFile(t, d, fileID, "/src", name, 1024)
		if _, err := d.ExecContext(ctx, `
			INSERT INTO virtual_fs_entries (file_id, source_path, target_path, status)
			VALUES (?, ?, ?, ?)`,
			fileID, "/src/"+name, "2024/June/"+name, status); err != nil {
			t.Fatal(err)
		}
	}

	n, err := approvedCount(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("approvedCount on a fresh db = %d, want 0 (rebuild is free)", n)
	}

	insert(1, "a.jpg", db.StatusProposed)
	if n, err = approvedCount(ctx, d); err != nil || n != 0 {
		t.Errorf("approvedCount = %d, %v; PROPOSED rows are not a confirmed plan", n, err)
	}

	insert(2, "b.jpg", db.StatusApproved)
	if n, err = approvedCount(ctx, d); err != nil || n != 1 {
		t.Errorf("approvedCount = %d, %v; want 1 so --rebuild refuses without --yes", n, err)
	}
}
