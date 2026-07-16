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

	// virtual_fs_entries references file_registry with no ON DELETE action,
	// so it must go before the registry delete
	result, err := tx.ExecContext(ctx, `DELETE FROM virtual_fs_entries`)
	if err != nil {
		return ResetResponse{}, fmt.Errorf("reset: delete vfs entries: %w", err)
	}
	count, _ = result.RowsAffected()
	resp.VFSEntriesDeleted = count

	result, err = tx.ExecContext(ctx, `DELETE FROM file_metadata`)
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

	// Factory wipe: confirmed folder names and anchors go too, so a reset
	// output dir behaves exactly like a brand-new one
	result, err = tx.ExecContext(ctx, `DELETE FROM user_labels`)
	if err != nil {
		return ResetResponse{}, fmt.Errorf("reset: delete user labels: %w", err)
	}
	count, _ = result.RowsAffected()
	resp.UserLabelsDeleted = count

	if err := tx.Commit(); err != nil {
		return ResetResponse{}, fmt.Errorf("reset: commit: %w", err)
	}

	return resp, nil
}
