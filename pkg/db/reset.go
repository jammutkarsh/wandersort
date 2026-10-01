package db

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// ResetCounts reports how many rows were deleted from each table
type ResetCounts struct {
	VFSEntriesDeleted   int64 `json:"vfsEntriesDeleted"`
	FileMetadataDeleted int64 `json:"fileMetadataDeleted"`
	FilesDeleted        int64 `json:"filesDeleted"`
	UserLabelsDeleted   int64 `json:"userLabelsDeleted"`
}

// ResetAll deletes all application data in FK-safe order within a transaction
func (d *DB) ResetAll(ctx context.Context) (ResetCounts, error) {
	var resp ResetCounts
	err := d.Writer.WriteSync(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
		// entries first so the count is theirs; folders after the entries
		// that reference them; the registry cascades to metadata and errors,
		// so metadata is counted first; user_labels too, so a reset library
		// behaves like a new one
		steps := []struct {
			what  string
			query string
			count *int64
		}{
			{"vfs entries", `DELETE FROM virtual_fs_entries`, &resp.VFSEntriesDeleted},
			{"folder nodes", `DELETE FROM folder_nodes`, nil},
			{"metadata", `DELETE FROM file_metadata`, &resp.FileMetadataDeleted},
			{"files", `DELETE FROM file_registry`, &resp.FilesDeleted},
			{"user labels", `DELETE FROM user_labels`, &resp.UserLabelsDeleted},
		}
		for _, step := range steps {
			result, err := tx.ExecContext(ctx, step.query)
			if err != nil {
				return fmt.Errorf("delete %s: %w", step.what, err)
			}
			if step.count != nil {
				*step.count, _ = result.RowsAffected()
			}
		}
		return nil
	})
	if err != nil {
		return ResetCounts{}, fmt.Errorf("reset: %w", err)
	}
	return resp, nil
}

// IsEmpty reports whether ResetAll would find nothing to delete. Resetting an
// empty database would overwrite the backup holding the earlier reset's data.
func (d *DB) IsEmpty(ctx context.Context) (bool, error) {
	var found bool
	err := d.SQL.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM virtual_fs_entries) OR
		EXISTS (SELECT 1 FROM folder_nodes) OR
		EXISTS (SELECT 1 FROM file_metadata) OR
		EXISTS (SELECT 1 FROM file_registry) OR
		EXISTS (SELECT 1 FROM user_labels)`).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("reset: check for data: %w", err)
	}
	return !found, nil
}

// PlacedCount is how many files the library holds; a reset forgets them.
func (d *DB) PlacedCount(ctx context.Context) (int, error) {
	var n int
	if err := d.SQL.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM file_registry WHERE placed = 1`).Scan(&n); err != nil {
		return 0, fmt.Errorf("reset: count placed files: %w", err)
	}
	return n, nil
}
