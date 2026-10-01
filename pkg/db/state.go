package db

import (
	"context"
	"fmt"
	stdpath "path"

	"github.com/jmoiron/sqlx"
)

// Where a planned file stands, with no status column: placed once
// file_registry.placed is set, failed while it has a TRANSFER error, pending
// otherwise. Phases use these helpers rather than writing the columns.

// PendingTransfer is the SQL predicate for "unplaced, no TRANSFER error".
// fileID is the caller's file id expression (vfe.file_id, ...).
func PendingTransfer(fileID string) string {
	return fmt.Sprintf(`(%[1]s IN (SELECT id FROM file_registry WHERE placed = 0)
		AND %[1]s NOT IN (SELECT file_id FROM errors WHERE stage = '%[2]s'))`, fileID, StageTransfer)
}

// MarkPlaced records a file as in the library at target (library-relative, so
// the database travels with the library), sets placed and clears its errors.
// Run it inside WriteSync.
func MarkPlaced(ctx context.Context, tx *sqlx.Tx, entryID, fileID int64, target string) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE virtual_fs_entries SET source_path = ?, target_path = ? WHERE id = ?`,
		target, target, entryID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE file_registry SET file_dir = ?, file_name = ?, placed = 1 WHERE id = ?`,
		stdpath.Dir(target), stdpath.Base(target), fileID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM errors WHERE file_id = ?`, fileID)
	return err
}

// MarkFailed records a failed transfer. The plan row stays where it was
// planned, so a retry lands where the user reviewed it.
func MarkFailed(ctx context.Context, tx *sqlx.Tx, fileID int64, op string, err error) error {
	return RecordError(ctx, tx, fileID, StageTransfer, op, err)
}

// Forget deletes files' registry rows; hash, plan and error rows cascade.
// Irreversible: back up first if unsure.
func Forget(ctx context.Context, tx *sqlx.Tx, fileIDs []int64) error {
	if len(fileIDs) == 0 {
		return nil
	}
	q, args, err := sqlx.In(`DELETE FROM file_registry WHERE id IN (?)`, fileIDs)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, tx.Rebind(q), args...); err != nil {
		return fmt.Errorf("forget files: %w", err)
	}
	return nil
}
