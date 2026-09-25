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

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/internal/review"
	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

func (a *app) newExecuteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "execute",
		Short: "Copy or move approved files into the output folder",
		Long: `Applies your 'wandersort review' edits to the plan, approves it, and
transfers every file from its source into <output>/<planned folder>. Copy is
the default and never touches a source file; --move deletes each source only
after its copy there is verified complete. Stops before changing anything if
the output has too little free space. Safe to re-run: a run interrupted
partway picks up where it left off next time.`,
		Example: `# Copy every approved file (safe, default)
wandersort execute

# See what would happen without touching anything
wandersort execute --dry-run

# Move instead of copy — prompts for confirmation unless --yes
wandersort execute --move`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runExecute(cmd)
		},
	}

	cmd.Flags().Bool(flagMove, false, "Move files instead of copying, deleting each source once its copy is verified")
	cmd.Flags().Bool(flagDryRun, false, "Report what would be transferred without touching anything (review edits not applied)")
	cmd.Flags().Bool(flagYes, false, "Skip the confirmation prompt --move asks for")
	return cmd
}

func (a *app) runExecute(cmd *cobra.Command) error {
	move, _ := cmd.Flags().GetBool(flagMove)
	dryRun, _ := cmd.Flags().GetBool(flagDryRun)
	yes, _ := cmd.Flags().GetBool(flagYes)

	if !a.libraryExists() {
		return fmt.Errorf("no database found — run 'wandersort add' first")
	}

	outputDir := a.Config.OutputDir()

	// Copy never touches a source, so it never asks. Move is the one thing in
	// this codebase that can delete the user's files — it asks unless the
	// caller already said --yes (a dry run deletes nothing either way).
	if move && !dryRun && !yes && !a.confirm(cmd, "Move files instead of copying?",
		"Each source file is deleted once its copy at the output is verified complete — this cannot be undone.") {
		return fmt.Errorf("execute cancelled")
	}

	ctx, cancel := interruptible()
	defer cancel()
	if err := a.openLibrary(ctx); err != nil {
		return err
	}
	defer a.closeDBs()

	// Spec D18: room for the whole plan first, so a refusal changes nothing;
	// then the review's draft goes into the plan and approves it, in one
	// transaction; only then does a file move.
	if !dryRun {
		if err := a.checkPlanFits(ctx, outputDir); err != nil {
			return err
		}
		if err := vfs.ApplyDraft(ctx, a.AppDB, outputDir); err != nil {
			return fmt.Errorf("apply review edits: %w", err)
		}
		// the plan is written: nothing is left to peek at
		if err := review.CleanPreviews(); err != nil {
			a.Log.Warn("could not remove the preview copies", "error", err)
		}
	}

	mode := execute.ModeCopy
	if move {
		mode = execute.ModeMove
	}
	rep, err := execute.Run(ctx, a.AppDB, a.Log, outputDir, execute.Options{Mode: mode, DryRun: dryRun})
	if errors.Is(err, context.Canceled) {
		// every file not yet reached is still pending; nothing is half-placed
		return fmt.Errorf("stopped after %d files — run 'wandersort execute' again to carry on from there", rep.Done)
	}
	if err != nil {
		return err
	}
	if rep.Done == 0 && rep.Failed == 0 {
		fmt.Fprintln(os.Stderr, "Nothing left to transfer — run 'wandersort add' to plan more files.")
		return nil
	}
	if rep.Failed > 0 {
		return fmt.Errorf("%d file(s) failed to transfer — see the log for why", rep.Failed)
	}
	fmt.Fprintln(os.Stderr, tui.OK.Render(fmt.Sprintf("Done: %d files.", rep.Done)))
	return nil
}

// checkPlanFits refuses a transfer the output volume can't hold: every file
// not yet transferred, proposed or approved — a review edit never changes a
// file's size, so the total is the same before and after the draft applies.
// An unreadable free-space figure lets the transfer run; each file still
// lands whole or not at all.
//
// ponytail: a same-volume move only renames and needs no free space, but this
// refuses one on a nearly-full disk too. Split the check by mode (or volume)
// if that's ever the transfer someone is blocked on.
func (a *app) checkPlanFits(ctx context.Context, outputDir string) error {
	needed, err := vfs.PendingBytes(ctx, a.AppDB)
	if err != nil {
		return err
	}
	if free, err := volume.FreeBytes(outputDir); err == nil && uint64(needed) > free {
		return fmt.Errorf("not enough free space: the plan needs %s, only %s free at the output — nothing was changed",
			volume.HumanBytes(uint64(needed)), volume.HumanBytes(free))
	}
	return nil
}
