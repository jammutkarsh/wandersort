package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/internal/review"
	"github.com/jammutkarsh/wandersort/pkg/core/library"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *app) newAdminDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Restore or reset the library database",
		Long: `The library database holds everything the library knows: which files it has
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
	// exactly one of --restore/--reset
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
		return fmt.Errorf("no backup at %s", backup)
	}
	if err != nil {
		return fmt.Errorf("backup %s: %w", backup, err)
	}
	taken := info.ModTime().Format("2006-01-02 15:04")

	yes, _ := cmd.Flags().GetBool(flagYes)
	if !yes && !a.confirm(cmd, "Restore the database from the backup of "+taken+"?",
		"Plan and edits go back to then. Copied files stay. The current database is kept as "+db.BeforeRestoreFileName+".") {
		return fmt.Errorf("restore cancelled")
	}

	// not openLibrary: Restore refuses while any connection, ours included,
	// has the database open
	a.logFile.Persist()
	l, err := a.lockOutput()
	if err != nil {
		return err
	}
	defer l.Unlock()

	ctx, cancel := interruptible()
	defer cancel()
	if err := library.Restore(ctx, a.Config.AppDBPath, a.Log); errors.Is(err, db.ErrInUse) {
		return fmt.Errorf("%w; close other apps using it and retry", err)
	} else if err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}

	fmt.Fprintln(os.Stderr, tui.OK.Render("Database restored from the backup of "+taken+"."))
	fmt.Fprintln(os.Stderr, tui.FaintTxt.Render("The database it replaced is kept as "+db.BeforeRestoreFileName+" in the library folder."))
	return nil
}

// resetDB empties the library's data but keeps its settings.
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

	// already empty: nothing to ask about
	if empty, err := a.AppDB.IsEmpty(ctx); err != nil {
		return err
	} else if empty {
		fmt.Fprintln(os.Stderr, "Nothing to reset — the database is already empty.")
		return nil
	}

	placed, err := a.AppDB.PlacedCount(ctx)
	if err != nil {
		return err
	}
	ask := tui.ResetAsk(placed)
	yes, _ := cmd.Flags().GetBool(flagYes)
	if !yes && !a.confirm(cmd, ask.Title, ask.Detail) {
		return fmt.Errorf("reset cancelled")
	}

	if err := a.resetLibrary(ctx); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, tui.OK.Render("Library database cleared."))
	fmt.Fprintln(os.Stderr, tui.FaintTxt.Render("Changed your mind? 'wandersort admin db --restore' brings it back."))
	return nil
}

// resetLibrary empties the open library's database (settings kept) and the preview copies.
func (a *app) resetLibrary(ctx context.Context) error {
	if err := library.Reset(ctx, a.AppDB, a.Config.OutputDir(), a.Log); err != nil {
		return err
	}
	// preview copies outlive a session, so a wipe has to take them too
	if err := review.CleanPreviews(); err != nil {
		a.Log.Warn("could not remove the preview copies", "error", err)
	}
	return nil
}
