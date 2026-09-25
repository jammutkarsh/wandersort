// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/internal/review"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *app) newAdminDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Restore or reset the library database",
		Long: `The library database holds everything WanderSort knows: which files it has
seen, which are duplicates, where each one is planned to go, and the edits you
made while organising. Your photos are not in it — they survive its loss, but
the structure you gave them does not.

--reset empties it, keeping your settings, and backs it up first.
--restore puts the backup back. One backup is kept, rewritten before each
transfer and before each reset.`,
		Example: `# Put the database back from its backup
wandersort admin db --restore

# Empty the database (asks first, backs up first)
wandersort admin db --reset

# Skip the confirmation either way
wandersort admin db --reset --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runAdminDB(cmd)
		},
	}

	cmd.Flags().Bool(flagRestore, false, "Put the database back from its backup")
	cmd.Flags().Bool(flagReset, false, "Delete everything in the database except your settings")
	cmd.Flags().Bool(flagYes, false, "Skip the confirmation prompt")
	// One or the other, never both and never neither: `admin db` on its own is
	// a noun with no verb, and cobra's own help is a better answer than a
	// hand-rolled guard.
	cmd.MarkFlagsMutuallyExclusive(flagRestore, flagReset)
	cmd.MarkFlagsOneRequired(flagRestore, flagReset)
	return cmd
}

// runAdminDB dispatches to the flag that was given; MarkFlagsOneRequired and
// MarkFlagsMutuallyExclusive guarantee exactly one of them was.
func (a *app) runAdminDB(cmd *cobra.Command) error {
	if reset, _ := cmd.Flags().GetBool(flagReset); reset {
		return a.resetDB(cmd)
	}
	return a.restoreDB(cmd)
}

func (a *app) restoreDB(cmd *cobra.Command) error {
	backup := filepath.Join(filepath.Dir(a.Config.AppDBPath), db.BackupFileName)
	info, err := os.Stat(backup)
	if os.IsNotExist(err) {
		return fmt.Errorf("no backup found at %s — one is written by 'wandersort execute' and by 'wandersort admin db --reset'", backup)
	}
	if err != nil {
		return fmt.Errorf("backup %s: %w", backup, err)
	}
	taken := info.ModTime().Format("2006-01-02 15:04")

	yes, _ := cmd.Flags().GetBool(flagYes)
	if !yes && !a.confirm(cmd, "Restore the database from the backup of "+taken+"?",
		"The plan and your edits go back to that point. Files already in the library stay where they are, and the current database is kept as "+db.BeforeRestoreFileName+" in case you want it back.") {
		return fmt.Errorf("restore cancelled")
	}

	// Not openLibrary: it would open the database this is about to replace,
	// and Restore refuses while any connection has it open, ours included.
	a.logFile.Persist()
	l, err := a.lockOutput()
	if err != nil {
		return err
	}
	defer l.Unlock()

	ctx, cancel := interruptible()
	defer cancel()
	if err := db.Restore(ctx, backup, a.Config.AppDBPath); errors.Is(err, db.ErrInUse) {
		return fmt.Errorf("%w — close it (a sqlite browser, a backup tool) and try again; nothing was changed", err)
	} else if err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}
	// Open once to prove the restored file is a usable library database.
	d, err := db.New(ctx, a.Config.AppDBPath, a.Log)
	if err != nil {
		return fmt.Errorf("restored database does not open: %w", err)
	}
	if err := d.Close(); err != nil {
		a.Log.Warn("closing restored database", "error", err)
	}

	fmt.Fprintln(os.Stderr, tui.OK.Render("Database restored from the backup of "+taken+"."))
	fmt.Fprintln(os.Stderr, tui.FaintTxt.Render("The database it replaced is kept as "+db.BeforeRestoreFileName+" in the library folder."))
	return nil
}

// resetDB empties the library's data, keeping the settings (db.ResetAll never
// touches library_settings): a factory wipe of what was scanned is not a
// request to forget which folders the user wants.
func (a *app) resetDB(cmd *cobra.Command) error {
	if !a.libraryExists() {
		return fmt.Errorf("no database found — nothing to reset")
	}

	ctx, cancel := interruptible()
	defer cancel()
	if err := a.openLibrary(ctx); err != nil {
		return err
	}
	defer a.closeDBs()

	// Already wiped: stop before the backup, which would replace the one
	// holding what the earlier reset deleted with an empty copy.
	empty, err := a.AppDB.IsEmpty(ctx)
	if err != nil {
		return err
	}
	if empty {
		fmt.Fprintln(os.Stderr, "Nothing to reset — the database is already empty.")
		return nil
	}

	yes, _ := cmd.Flags().GetBool(flagYes)
	if !yes && !a.confirm(cmd, "Delete everything in the library database?",
		"What was scanned, the duplicates found, the plan and your edits. Your settings stay. 'wandersort admin db --restore' brings the rest back until the next transfer or reset.") {
		return fmt.Errorf("reset cancelled")
	}

	// The backup is what makes this undoable; no backup, no wipe.
	outDir := filepath.Dir(a.Config.AppDBPath)
	if err := a.AppDB.Backup(ctx, filepath.Join(outDir, db.BackupFileName)); err != nil {
		return fmt.Errorf("back up database before reset: %w", err)
	}

	if _, err := a.AppDB.ResetAll(ctx); err != nil {
		return fmt.Errorf("reset failed: %w", err)
	}

	if err := a.AppDB.Optimize(ctx); err != nil {
		a.Log.Warn("database optimization after reset failed", "error", err)
	}

	// The edits describe a plan that no longer exists.
	if err := vfs.RemoveDraft(outDir); err != nil {
		a.Log.Warn("could not remove the review draft", "error", err)
	}

	// Preview copies outlive a session, so a wipe has to take them too.
	if err := review.CleanPreviews(); err != nil {
		a.Log.Warn("could not remove the preview copies", "error", err)
	}

	fmt.Fprintln(os.Stderr, tui.OK.Render("Library database cleared."))
	fmt.Fprintln(os.Stderr, tui.FaintTxt.Render("Changed your mind? 'wandersort admin db --restore' brings it back."))
	return nil
}
