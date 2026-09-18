// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/spf13/cobra"
)

func (a *app) newRecoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Restore the library database from its backup",
		Long: `Replaces the library database with .wandersort.db.bak, the backup taken
before the last 'wandersort execute' or 'wandersort reset --db'. The plan, your
review edits and approvals go back to that point; anything since is lost. The
backup itself is kept. Asks for confirmation unless --yes is given.`,
		Example: `# Restore the database (prompts for confirmation)
wandersort recover

# Skip the confirmation prompt
wandersort recover --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runRecover(cmd)
		},
	}
	cmd.Flags().Bool(flagYes, false, "Skip confirmation prompt")
	return cmd
}

func (a *app) runRecover(cmd *cobra.Command) error {
	backup := filepath.Join(filepath.Dir(a.Config.AppDBPath), db.BackupFileName)
	info, err := os.Stat(backup)
	if os.IsNotExist(err) {
		return fmt.Errorf("no backup found at %s — one is written by 'wandersort execute' and 'wandersort reset --db'", backup)
	}
	if err != nil {
		return fmt.Errorf("backup %s: %w", backup, err)
	}
	taken := info.ModTime().Format("2006-01-02 15:04")

	yes, _ := cmd.Flags().GetBool(flagYes)
	if !yes && !a.confirm(cmd, "Restore the database from the backup of "+taken+"?",
		"The plan, review edits and approvals go back to that point; anything since is lost. Files an execute run already placed stay where they are.") {
		return fmt.Errorf("recover cancelled")
	}

	// Not openLibrary: it would open the database this is about to replace,
	// and Restore refuses while any connection has it open, ours included.
	a.logFile.Persist()
	l, err := a.lockOutput()
	if err != nil {
		return err
	}
	defer l.Unlock()

	ctx := context.Background()
	if err := db.Restore(ctx, backup, a.Config.AppDBPath); errors.Is(err, db.ErrInUse) {
		return fmt.Errorf("%w — close it (a sqlite browser, a backup tool) and try again; nothing was changed", err)
	} else if err != nil {
		return fmt.Errorf("recover failed: %w", err)
	}
	// Open once to prove the restored file is a usable library database.
	d, err := db.New(ctx, a.Config.AppDBPath, db.AppDB, a.Log)
	if err != nil {
		return fmt.Errorf("restored database does not open: %w", err)
	}
	if err := d.Close(); err != nil {
		a.Log.Warn("closing restored database", "error", err)
	}

	fmt.Fprintln(os.Stderr, tui.OK.Render("Database restored from the backup of "+taken+"."))
	return nil
}
