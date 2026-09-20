// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"fmt"
)

// ResetCounts reports how many rows were deleted from each table
type ResetCounts struct {
	VFSEntriesDeleted   int64 `json:"vfsEntriesDeleted"`
	FileMetadataDeleted int64 `json:"fileMetadataDeleted"`
	FilesDeleted        int64 `json:"filesDeleted"`
	UserLabelsDeleted   int64 `json:"userLabelsDeleted"`
}

// ResetAll deletes all application data in FK-safe order within a transaction
func (d *DB) ResetAll(ctx context.Context) (ResetCounts, error) {
	var resp ResetCounts

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return ResetCounts{}, fmt.Errorf("reset: begin tx: %w", err)
	}
	defer tx.Rollback()

	var count int64

	// entries first so the count is theirs; the cascade would delete them anyway
	result, err := tx.ExecContext(ctx, `DELETE FROM virtual_fs_entries`)
	if err != nil {
		return ResetCounts{}, fmt.Errorf("reset: delete vfs entries: %w", err)
	}
	count, _ = result.RowsAffected()
	resp.VFSEntriesDeleted = count

	// the plan's folders go after the entries that reference them
	if _, err := tx.ExecContext(ctx, `DELETE FROM folder_nodes`); err != nil {
		return ResetCounts{}, fmt.Errorf("reset: delete folder nodes: %w", err)
	}

	// deleting the registry cascades to metadata and errors; count metadata first
	result, err = tx.ExecContext(ctx, `DELETE FROM file_metadata`)
	if err != nil {
		return ResetCounts{}, fmt.Errorf("reset: delete metadata: %w", err)
	}
	count, _ = result.RowsAffected()
	resp.FileMetadataDeleted = count

	result, err = tx.ExecContext(ctx, `DELETE FROM file_registry`)
	if err != nil {
		return ResetCounts{}, fmt.Errorf("reset: delete files: %w", err)
	}
	count, _ = result.RowsAffected()
	resp.FilesDeleted = count

	// Factory wipe: confirmed folder names and anchors go too, so a reset
	// output dir behaves exactly like a brand-new one
	result, err = tx.ExecContext(ctx, `DELETE FROM user_labels`)
	if err != nil {
		return ResetCounts{}, fmt.Errorf("reset: delete user labels: %w", err)
	}
	count, _ = result.RowsAffected()
	resp.UserLabelsDeleted = count

	if err := tx.Commit(); err != nil {
		return ResetCounts{}, fmt.Errorf("reset: commit: %w", err)
	}

	return resp, nil
}

// IsEmpty reports whether ResetAll would find anything to delete. A reset of
// an empty database must not run at all: its backup would overwrite the one
// holding the data an earlier reset wiped.
func (d *DB) IsEmpty(ctx context.Context) (bool, error) {
	var found bool
	err := d.SQL.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM virtual_fs_entries) OR
		EXISTS (SELECT 1 FROM folder_nodes) OR
		EXISTS (SELECT 1 FROM file_metadata) OR
		EXISTS (SELECT 1 FROM file_registry) OR
		EXISTS (SELECT 1 FROM user_labels)`).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("reset: check for data: %w", err)
	}
	return !found, nil
}
