// Package library owns the whole-library maintenance steps that span the
// database and the review draft: reset and restore.
package library

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// ErrNothingToReset means the database was already empty. Reset then writes
// nothing: a backup would overwrite the one holding the earlier reset's data.
var ErrNothingToReset = errors.New("the database is already empty")

// Reset backs the library database up, empties it (settings are kept) and
// drops the review draft, whose edits describe a plan that no longer exists.
// No backup, no wipe.
func Reset(ctx context.Context, database *db.DB, outputDir string, log logger.Logger) error {
	empty, err := database.IsEmpty(ctx)
	if err != nil {
		return err
	}
	if empty {
		return ErrNothingToReset
	}
	if err := database.Backup(ctx, filepath.Join(outputDir, db.BackupFileName)); err != nil {
		return fmt.Errorf("back up database before reset: %w", err)
	}
	if _, err := database.ResetAll(ctx); err != nil {
		return fmt.Errorf("reset failed: %w", err)
	}
	if err := database.Optimize(ctx); err != nil {
		log.Warn("database optimization after reset failed", "error", err)
	}
	if err := vfs.RemoveDraft(outputDir); err != nil {
		log.Warn("could not remove the review draft", "error", err)
	}
	return nil
}

// Restore puts the backup beside the library database at live back over it,
// checks the result opens as a library, and drops the review draft: its edits
// were made against a newer plan. The caller holds the output lock and has no
// connection open (db.ErrInUse otherwise).
func Restore(ctx context.Context, live string, log logger.Logger) error {
	outputDir := filepath.Dir(live)
	if err := db.Restore(ctx, filepath.Join(outputDir, db.BackupFileName), live); err != nil {
		return err
	}
	d, err := db.New(ctx, live, log)
	if err != nil {
		return fmt.Errorf("restored database does not open: %w", err)
	}
	if err := d.Close(); err != nil {
		log.Warn("closing restored database", "error", err)
	}
	if err := vfs.RemoveDraft(outputDir); err != nil {
		log.Warn("could not remove the review draft", "error", err)
	}
	return nil
}
