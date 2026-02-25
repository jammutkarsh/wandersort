package hash

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetProgress(ctx context.Context, sessionID uuid.UUID) (*HashProgressResponse, error) {
	var p HashProgressResponse
	p.SessionID = sessionID.String()

	var completedAt *time.Time
	err := r.db.QueryRow(ctx, `
		SELECT status, files_discovered, COALESCE(files_hashed, 0), started_at, completed_at
		FROM scan_sessions WHERE id = $1
	`, sessionID).Scan(&p.Status, &p.FilesDiscovered, &p.FilesHashed, &p.StartedAt, &completedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("session not found")
		}
		return nil, fmt.Errorf("query session: %w", err)
	}
	p.CompletedAt = completedAt

	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM file_registry
		WHERE scan_session_id = $1 AND scan_status = 'ERROR'
	`, sessionID).Scan(&p.FilesErrored); err != nil {
		return nil, fmt.Errorf("query errored files: %w", err)
	}

	if p.FilesDiscovered > 0 {
		p.PercentComplete = float64(p.FilesHashed) / float64(p.FilesDiscovered) * 100
	}

	return &p, nil
}

func (r *Repository) GetStats(ctx context.Context) (*HashStatsResponse, error) {
	var s HashStatsResponse
	err := r.db.QueryRow(ctx, `
		WITH
			group_stats AS (
				SELECT
					COUNT(*)                                            AS total_groups,
					COUNT(*) FILTER (WHERE total_copies > 1)            AS groups_with_dupes,
					COUNT(*) FILTER (WHERE master_file_id IS NOT NULL)  AS masters_elected
				FROM content_groups
			),
			member_stats AS (
				SELECT
					COUNT(*)                                            AS total_files,
					COUNT(*) FILTER (WHERE is_master = FALSE)           AS duplicate_files
				FROM content_group_members
			)
		SELECT total_groups, groups_with_dupes, masters_elected, total_files, duplicate_files
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
