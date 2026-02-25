package admin

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Reset deletes all application data in FK-safe order in a single CTE statement.
func (r *Repository) Reset(ctx context.Context) (ResetResponse, error) {
	var resp ResetResponse

	err := r.db.QueryRow(ctx, `
		WITH
			del_members AS (
				DELETE FROM content_group_members
				RETURNING 1
			),
			del_groups AS (
				DELETE FROM content_groups
				RETURNING 1
			),
			del_files AS (
				DELETE FROM file_registry
				RETURNING 1
			),
			del_sessions AS (
				DELETE FROM scan_sessions
				RETURNING 1
			)
		SELECT
			(SELECT COUNT(*) FROM del_members)  AS group_members_deleted,
			(SELECT COUNT(*) FROM del_groups)   AS content_groups_deleted,
			(SELECT COUNT(*) FROM del_files)    AS files_deleted,
			(SELECT COUNT(*) FROM del_sessions) AS scan_sessions_deleted
	`).Scan(
		&resp.GroupMembersDeleted,
		&resp.ContentGroupsDeleted,
		&resp.FilesDeleted,
		&resp.ScanSessionsDeleted,
	)
	if err != nil {
		return ResetResponse{}, fmt.Errorf("reset: %w", err)
	}

	return resp, nil
}
