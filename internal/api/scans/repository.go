package scans

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetScanStatus(ctx context.Context, sessionID uuid.UUID) (*scanner.ScanSession, error) {
	var session scanner.ScanSession
	var rootPathsJSON []byte

	err := r.db.QueryRow(ctx, `
		SELECT id, started_at, completed_at, status, root_paths,
		       files_discovered, files_skipped, files_new, files_modified,
		       errors_encountered, last_error
		FROM scan_sessions
		WHERE id = $1
	`, sessionID).Scan(
		&session.ID,
		&session.StartedAt,
		&session.CompletedAt,
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
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("scan session not found")
		}
		return nil, fmt.Errorf("get scan status: %w", err)
	}

	if err := json.Unmarshal(rootPathsJSON, &session.RootPaths); err != nil {
		return nil, fmt.Errorf("unmarshal root paths: %w", err)
	}

	return &session, nil
}

func (r *Repository) GetFileCount(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM file_registry`).Scan(&count)
	return count, err
}
