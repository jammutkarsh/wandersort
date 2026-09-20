// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/internal/review"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/spf13/cobra"
)

func (a *app) newResetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Clear cached previews, or with --db all wandersort scan data",
		Long: `Clears the cached preview copies 'wandersort review' makes when you peek
into a folder. Nothing else is touched, so it never asks.

With --db it also clears the library database — scan history, file index,
duplicate results, the plan and your review edits. That asks for confirmation
unless --yes is given, and backs the database up first: 'wandersort recover'
brings it back.`,
		Example: `# Clear cached preview copies
wandersort reset

# Also delete all scan data (prompts for confirmation)
wandersort reset --db

# Skip the confirmation prompt
wandersort reset --db --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReset(cmd)
		},
	}

	cmd.Flags().Bool(flagDB, false, "Also delete the library database (asks first)")
	cmd.Flags().Bool(flagYes, false, "Skip the confirmation prompt --db asks for")
	return cmd
}

func (a *app) runReset(cmd *cobra.Command) error {
	if wipeDB, _ := cmd.Flags().GetBool(flagDB); wipeDB {
		return a.resetDB(cmd)
	}
	if err := review.CleanPreviews(); err != nil {
		return fmt.Errorf("could not remove the preview copies: %w", err)
	}
	fmt.Fprintln(os.Stderr, tui.OK.Render("Preview cache cleared."))
	fmt.Fprintln(os.Stderr, tui.FaintTxt.Render("To delete the library database too, run 'wandersort reset --db'."))
	return nil
}

func (a *app) resetDB(cmd *cobra.Command) error {
	if !a.libraryExists() {
		return fmt.Errorf("no database found — nothing to reset")
	}

	ctx := context.Background()
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
	if !yes && !a.confirm(cmd, "Delete all wandersort data?",
		"Scan history, file index, duplicate results, the plan and your review edits. 'wandersort recover' can bring them back until the next copy/move or reset.") {
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

	// The review edits describe a proposal that no longer exists.
	if err := vfs.RemoveDraft(outDir); err != nil {
		a.Log.Warn("could not remove the review draft", "error", err)
	}

	// Preview copies outlive a review session, so a wipe has to take them too.
	if err := review.CleanPreviews(); err != nil {
		a.Log.Warn("could not remove the preview copies", "error", err)
	}

	fmt.Fprintln(os.Stderr, tui.OK.Render("All wandersort data deleted."))
	fmt.Fprintln(os.Stderr, tui.FaintTxt.Render("Changed your mind? 'wandersort recover' brings it back."))
	return nil
}
