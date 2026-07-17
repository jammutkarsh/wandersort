package pipeline

import (
	"context"

	"github.com/jammutkarsh/wandersort/pkg/db"
)

type Repository struct {
	db *db.DB
}

func NewRepository(database *db.DB) *Repository {
	return &Repository{db: database}
}

// GetFileCount returns the number of files tracked by the scanner and the hasher
func (r *Repository) GetFileCount(ctx context.Context) (FileCountResponse, error) {
	var resp FileCountResponse

	if err := r.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM file_registry WHERE deleted_at IS NULL`,
	).Scan(&resp.FilesScanned); err != nil {
		return FileCountResponse{}, err
	}

	if err := r.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM file_metadata fm
		JOIN file_registry fr ON fr.id = fm.file_id
		WHERE fr.deleted_at IS NULL`,
	).Scan(&resp.FilesHashed); err != nil {
		return FileCountResponse{}, err
	}

	return resp, nil
}
