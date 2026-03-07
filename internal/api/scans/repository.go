package scans

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/db"
)

type Repository struct {
	db *db.DB
}

func NewRepository(db *db.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetScanStatus(ctx context.Context, sessionID uuid.UUID) (*ScanSession, error) {
	var session ScanSession
	var rootPathsJSON string
	var startedAt string
	var completedAt *string

	err := r.db.QueryRowContext(ctx, `
		SELECT id, started_at, completed_at, status, root_paths,
		       files_discovered, files_skipped, files_new, files_modified,
		       errors_encountered, last_error
		FROM scan_sessions
		WHERE id = ?
	`, sessionID.String()).Scan(
		&session.ID,
		&startedAt,
		&completedAt,
		&session.Status,
		&rootPathsJSON,
		&session.FilesDiscovered,
		&session.FilesSkipped,
		&session.FilesNew,
		&session.FilesModified,
		&session.ErrorsEncountered,
		&session.LastError,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("scan session not found")
		}
		return nil, fmt.Errorf("get scan status: %w", err)
	}

	session.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
	if completedAt != nil {
		t, _ := time.Parse(time.RFC3339, *completedAt)
		session.CompletedAt = &t
	}

	if err := json.Unmarshal([]byte(rootPathsJSON), &session.RootPaths); err != nil {
		return nil, fmt.Errorf("unmarshal root paths: %w", err)
	}

	return &session, nil
}

func (r *Repository) GetFileCount(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_registry`).Scan(&count)
	return count, err
}
