package pipeline

import (
	"context"

	"github.com/jammutkarsh/wandersort/pkg/db"
)

type Repository struct {
	db *db.DB
}

func NewRepository(d *db.DB) *Repository {
	return &Repository{db: d}
}

// GetFileCount returns the number of files tracked by the scanner and the hasher.
func (r *Repository) GetFileCount(ctx context.Context) (FileCountResponse, error) {
	var resp FileCountResponse

	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM file_registry`,
	).Scan(&resp.FilesScanned); err != nil {
		return FileCountResponse{}, err
	}

	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM content_group_members`,
	).Scan(&resp.FilesHashed); err != nil {
		return FileCountResponse{}, err
	}

	return resp, nil
}
