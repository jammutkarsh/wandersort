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

// Reset deletes all application data in FK-safe order within a transaction.
func (r *Repository) Reset(ctx context.Context) (ResetResponse, error) {
	var resp ResetResponse

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ResetResponse{}, fmt.Errorf("reset: begin tx: %w", err)
	}
	defer tx.Rollback()

	var count int64

	result, err := tx.ExecContext(ctx, `DELETE FROM content_group_members`)
	if err != nil {
		return ResetResponse{}, fmt.Errorf("reset: delete members: %w", err)
	}
	count, _ = result.RowsAffected()
	resp.GroupMembersDeleted = count

	result, err = tx.ExecContext(ctx, `DELETE FROM content_groups`)
	if err != nil {
		return ResetResponse{}, fmt.Errorf("reset: delete groups: %w", err)
	}
	count, _ = result.RowsAffected()
	resp.ContentGroupsDeleted = count

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
