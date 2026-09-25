// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"fmt"
	stdpath "path"

	"github.com/jmoiron/sqlx"
)

// Where a planned file stands. There is no status column: a file is placed
// once file_registry.placed says so, failed while it has a TRANSFER row in
// errors, and pending otherwise. The predicate and the transitions below are
// the one spelling of that; every phase composes them rather than writing the
// columns itself.

// PendingTransfer is the SQL predicate for "this file has not been
// transferred and did not fail to be": unplaced, no TRANSFER row in errors.
// fileID is the file id expression of the caller's query (vfe.file_id, ...).
// Its negation is the decided set — placed or failed — which a re-plan leaves
// alone.
func PendingTransfer(fileID string) string {
	return fmt.Sprintf(`(%[1]s IN (SELECT id FROM file_registry WHERE placed = 0)
		AND %[1]s NOT IN (SELECT file_id FROM errors WHERE stage = '%[2]s'))`, fileID, StageTransfer)
}

// MarkPlaced records that a file is in the library at target, a
// library-relative path (spec D9/D10): its plan row and its registry row both
// point there from now on, so a database that travels with the library does
// not depend on where it is mounted. It sets placed and clears the file's
// error rows. Run it inside WriteSync: this is the one row whose absence the
// user pays for in photos.
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

// Forget deletes files' records: the registry rows, and with them (ON DELETE
// CASCADE) their hash, plan and error rows. Irreversible — the caller decides
// the file is no longer worth knowing about, and backs up first if it cannot
// be sure.
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
