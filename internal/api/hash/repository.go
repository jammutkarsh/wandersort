package hash

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/internal/api"
	"github.com/jammutkarsh/wandersort/pkg/db"
)

type Repository struct {
	db *db.DB
}

func NewRepository(db *db.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetProgress(ctx context.Context, sessionID uuid.UUID) (*HashProgressResponse, error) {
	var p HashProgressResponse
	p.SessionID = sessionID.String()

	var completedAt *string
	var startedAt string
	err := r.db.QueryRowContext(ctx, `
		SELECT status, files_discovered, COALESCE(files_hashed, 0), started_at, completed_at
		FROM scan_sessions WHERE id = ?
	`, sessionID.String()).Scan(&p.Status, &p.FilesDiscovered, &p.FilesHashed, &startedAt, &completedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("session not found")
		}
		return nil, fmt.Errorf("query session: %w", err)
	}

	parsedStartedAt, err := api.ParseDBTime(startedAt)
	if err != nil {
		return nil, fmt.Errorf("parse started_at: %w", err)
	}
	p.StartedAt = parsedStartedAt
	if completedAt != nil {
		t, err := api.ParseDBTime(*completedAt)
		if err != nil {
			return nil, fmt.Errorf("parse completed_at: %w", err)
		}
		p.CompletedAt = &t
	}

	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM file_registry
		WHERE scan_session_id = ? AND scan_status = 'ERROR'
	`, sessionID.String()).Scan(&p.FilesErrored); err != nil {
		return nil, fmt.Errorf("query errored files: %w", err)
	}

	if p.FilesDiscovered > 0 {
		p.PercentComplete = float64(p.FilesHashed) / float64(p.FilesDiscovered) * 100
	}

	return &p, nil
}

func (r *Repository) GetStats(ctx context.Context) (*HashStatsResponse, error) {
	var s HashStatsResponse
	err := r.db.QueryRowContext(ctx, `
		WITH
			group_stats AS (
				SELECT
					COUNT(*)                                                       AS total_groups,
					SUM(CASE WHEN total_copies > 1 THEN 1 ELSE 0 END)             AS groups_with_dupes,
					SUM(CASE WHEN master_file_id IS NOT NULL THEN 1 ELSE 0 END)    AS masters_elected
				FROM content_groups
			),
			member_stats AS (
				SELECT
					COUNT(*)                                              AS total_files,
					SUM(CASE WHEN is_master = 0 THEN 1 ELSE 0 END)       AS duplicate_files
				FROM content_group_members
			)
		SELECT
			COALESCE(total_groups, 0),
			COALESCE(groups_with_dupes, 0),
			COALESCE(masters_elected, 0),
			COALESCE(total_files, 0),
			COALESCE(duplicate_files, 0)
		FROM group_stats, member_stats
	`).Scan(
		&s.TotalGroups,
		&s.GroupsWithDupes,
		&s.MastersElected,
		&s.TotalFiles,
		&s.DuplicateFiles,
	)
	if err != nil {
		return nil, fmt.Errorf("query group stats: %w", err)
	}
	return &s, nil
}
