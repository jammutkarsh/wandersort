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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/internal/review"
	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func (a *app) newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Review and correct the proposed folder structure",
		Long: `Walks the folder hierarchy proposed by the last scan so you can rename,
merge, drop and flatten folders before anything is moved. Names you type are
remembered and offered as rename completions in later reviews.`,
		Example: `# Review interactively
wandersort review

# Skip the TUI: confirm the proposed hierarchy as-is
wandersort review --yes

# Confirm and copy every approved file to the output in one step
wandersort review --yes --copy`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReview(cmd)
		},
	}

	cmd.Flags().Bool(flagYes, false, "Skip the interactive review: confirm the proposed hierarchy as-is")
	cmd.Flags().Bool(flagCopy, false, "With --yes, also copy every approved file to the output afterwards")
	cmd.Flags().Bool(flagMove, false, "With --yes, also move every approved file to the output afterwards (deletes each source once verified)")
	cmd.Flags().Bool(flagDryRun, false, "With --copy/--move, report what would be transferred without touching anything")
	return cmd
}

func (a *app) runReview(cmd *cobra.Command) error {
	yes, _ := cmd.Flags().GetBool(flagYes)
	copyNow, _ := cmd.Flags().GetBool(flagCopy)
	moveNow, _ := cmd.Flags().GetBool(flagMove)
	dryRun, _ := cmd.Flags().GetBool(flagDryRun)
	if copyNow && moveNow {
		return fmt.Errorf("--copy and --move are mutually exclusive")
	}

	// --move here asks nothing — --yes already means "no prompts, I know what
	// I'm doing", the same contract --yes has everywhere else in this command.
	switch {
	case yes:
		return a.confirmReviewAll(copyNow || moveNow, moveNow, dryRun)
	case a.isTuiEnabled(cmd):
		// Opens the app on the review tab — the same session a bare
		// `wandersort` gives, so a reviewer who finds the folders wrong can fix
		// the settings and come back without relaunching. The shell opens the
		// lock, the database and the tree itself, and reports a library with
		// nothing to review on screen rather than refusing to start: there is a
		// scan tab one ctrl+t away, which is exactly what that user needs.
		return a.runShell(shellStart{tab: tabReview})
	default:
		// An alt-screen review in a pipe was never usable; say so instead of
		// drawing one into a file.
		return fmt.Errorf("review needs an interactive terminal — use 'wandersort review --yes' to confirm the proposal as-is")
	}
}

// confirmReviewAll is `review --yes`: no TUI, so the lock, the database and
// the proposal work all run inline here, and a missing library is a hard
// error rather than a screen. A settings change re-plans right here, exactly
// as it would opening the interactive review — there's nobody to ask, so it
// just happens. transfer requests execute.Run right after Confirm, in the
// same process — the lock covering both is what makes "approve then move" one
// atomic-looking step for a script; move picks the mode, dryRun makes either
// one a report instead of a write.
func (a *app) confirmReviewAll(transfer, move, dryRun bool) error {
	if _, err := os.Stat(a.Config.AppDBPath); os.IsNotExist(err) {
		return fmt.Errorf("no database found — run 'wandersort scan' first")
	}

	ctx := context.Background()
	if err := a.openLibrary(ctx); err != nil {
		return err
	}
	defer a.closeDBs()

	// Only the vfs phase might run here — no metadata phase, so no exiftool
	// needed; ask only for what this command needs.
	a.Deps = a.newDeps(nil)
	a.Deps.StartLocationOnly(ctx, nil)

	outputDir := filepath.Dir(a.Config.AppDBPath)
	var tree []vfs.Node
	var err error
	if a.settingsChanged(outputDir) {
		if tree, err = a.rebuildTree(ctx); err != nil {
			return err
		}
	} else if tree, err = vfs.BuildTree(ctx, a.AppDB); err != nil {
		return err
	}
	if len(tree) == 0 {
		return fmt.Errorf("no proposal to review — run 'wandersort scan' first")
	}

	if err := review.ConfirmAll(ctx, review.Options{
		DB: a.AppDB, Tree: tree, Log: a.Log, OutputDir: outputDir,
	}); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Folder structure approved.")
	if !transfer {
		fmt.Fprintln(os.Stderr, "Run 'wandersort execute' to copy or move the files, or re-run with --copy/--move.")
		return nil
	}

	mode := execute.ModeCopy
	if move {
		mode = execute.ModeMove
	}
	rep, err := execute.Run(ctx, a.AppDB, a.Log, outputDir, execute.Options{Mode: mode, DryRun: dryRun})
	if err != nil {
		return err
	}
	if rep.Failed > 0 {
		return fmt.Errorf("%d file(s) failed to transfer — see the log for why", rep.Failed)
	}
	return nil
}

// settingsChanged reports that the settings moved since the current proposal
// was built. No stamp file (a proposal from before stamping) is never a
// change — there is nothing to compare against. Net-zero edits in the wizard
// are not a change either: the comparison is the fingerprint of the settings
// themselves, not "did the user open the wizard".
func (a *app) settingsChanged(outputDir string) bool {
	stamp, ok, err := vfs.ReadStamp(outputDir)
	if err != nil {
		a.Log.Warn("Could not read the settings this proposal used", "error", err)
		return false
	}
	return ok && stamp != vfs.ConfigStamp(vfs.ConfigFor(a.Config))
}

// rebuildTree re-proposes the whole hierarchy from the settings as they stand
// right now and returns the new tree — called whenever a proposal turns out
// to be built under settings that have since moved: opening the review over a
// stale stamp, confirming with --yes over one, or a wizard save that changes
// something while a review is on screen (see configSaved). `a.Config` is
// re-resolved on every wizard save (see app.reloadConfig), so "right now"
// really is what the user last saved.
//
// It reopens every approved-but-not-transferred row first, so a re-plan
// really does replan everything. Keeping them was a reported bug: change the
// settings, and the rows already signed off still read `✓ saved` while
// holding folders the new settings would never have proposed. An approval is
// given to a specific plan; replacing that plan takes it back.
func (a *app) rebuildTree(ctx context.Context) ([]vfs.Node, error) {
	resolver, err := a.Deps.Location()
	if err != nil {
		return nil, fmt.Errorf("dependencies: %w", err)
	}
	a.Log.Info("Settings changed — re-proposing the folder structure", logger.UserKey, true)
	if err := vfs.ReopenPlan(ctx, a.AppDB); err != nil {
		return nil, err
	}
	if _, err := vfs.Propose(ctx, a.AppDB, resolver, a.Config, a.Log); err != nil {
		return nil, fmt.Errorf("re-plan proposal: %w", err)
	}
	return vfs.BuildTree(ctx, a.AppDB)
}

// newReviewScreen builds the review screen over the current proposal, reusing
// the scan's already-open DB and Deps — no lock/DB re-init needed. A stale
// stamp re-plans right here, before the screen ever renders, rather than
// raising a question on screen — see rebuildTree.
//
// An empty tree (after that check) means every master is already DONE from an
// earlier execute — a fully organized library, not a plan to rebuild; there's
// nothing to re-propose in that case, so this doesn't try.
func (a *app) newReviewScreen(ctx context.Context) (tea.Model, error) {
	// Doesn't block: a.Deps was started by the scan and vfs already ran, so
	// the location download has resolved by now. Autocomplete just degrades
	// gracefully without a resolver if it somehow hasn't.
	resolver, err := a.Deps.Location()
	if err != nil {
		a.Log.Warn("Location resolver unavailable, rename completions disabled", "error", err)
	}
	outputDir := filepath.Dir(a.Config.AppDBPath)
	var tree []vfs.Node
	if a.settingsChanged(outputDir) {
		if tree, err = a.rebuildTree(ctx); err != nil {
			return nil, err
		}
	} else if tree, err = vfs.BuildTree(ctx, a.AppDB); err != nil {
		return nil, err
	}
	if len(tree) == 0 {
		return nil, fmt.Errorf("everything here is already organized — nothing left to review")
	}
	return review.Screen(ctx, review.Options{
		DB:        a.AppDB,
		Tree:      tree,
		Resolver:  resolver,
		Log:       a.Log,
		OutputDir: outputDir,
	}), nil
}

// reportReviewOutcome reports how the embedded review ended, and hands back
// the line it logged — the shell shows it above the next folder input, since
// the session carries on past the review that produced it.
//
// A transfer takes priority over Confirmed: [x]/[X] can copy or move real
// files at any point in the session, including on a path that never sets
// Confirmed (a transfer can end the review on its own once it empties the
// tree, or a discard/ctrl+c can land before a background transfer's own
// reload does) — reporting "cancelled" or bare "approved" over a session that
// actually wrote files would be a lie about what just happened on disk.
func (a *app) reportReviewOutcome(res review.Result) (string, error) {
	if res.Err != nil {
		return "", fmt.Errorf("save plan: %w", res.Err)
	}
	note := "Review cancelled — nothing changed"
	switch {
	case res.TransferDone > 0 || res.TransferFailed > 0:
		note = fmt.Sprintf("Transferred %d file(s) to the output.", res.TransferDone)
		if res.TransferFailed > 0 {
			note = fmt.Sprintf("Transferred %d file(s) to the output, %d failed — see the log.", res.TransferDone, res.TransferFailed)
		}
	case res.Confirmed:
		note = "Folder structure approved."
	}
	a.Log.Info(note, logger.UserKey, true)
	return note, nil
}
