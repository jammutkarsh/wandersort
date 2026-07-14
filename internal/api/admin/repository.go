// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"context"
	"fmt"

	"github.com/jammutkarsh/wandersort/pkg/db"
)

type Repository struct {
	db *db.DB
}

func NewRepository(db *db.DB) *Repository {
	return &Repository{db: db}
}

// Reset deletes all application data in FK-safe order within a transaction
func (r *Repository) Reset(ctx context.Context) (ResetResponse, error) {
	var resp ResetResponse

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ResetResponse{}, fmt.Errorf("reset: begin tx: %w", err)
	}
	defer tx.Rollback()

	var count int64

	result, err := tx.ExecContext(ctx, `DELETE FROM file_metadata`)
	if err != nil {
		return ResetResponse{}, fmt.Errorf("reset: delete metadata: %w", err)
	}
	count, _ = result.RowsAffected()
	resp.FileMetadataDeleted = count

	result, err = tx.ExecContext(ctx, `DELETE FROM file_registry`)
	if err != nil {
		return ResetResponse{}, fmt.Errorf("reset: delete files: %w", err)
	}
	count, _ = result.RowsAffected()
	resp.FilesDeleted = count

	result, err = tx.ExecContext(ctx, `DELETE FROM scan_sessions`)
	if err != nil {
		return ResetResponse{}, fmt.Errorf("reset: delete sessions: %w", err)
	}
	count, _ = result.RowsAffected()
	resp.ScanSessionsDeleted = count

	if err := tx.Commit(); err != nil {
		return ResetResponse{}, fmt.Errorf("reset: commit: %w", err)
	}

	return resp, nil
}
