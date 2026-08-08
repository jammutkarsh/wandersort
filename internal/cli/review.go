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

# Re-propose the hierarchy with the current config.yaml rules first
wandersort review --rebuild`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReview(cmd)
		},
	}

	cmd.Flags().Bool(flagYes, false, "Skip the interactive review: confirm the proposed hierarchy as-is")
	cmd.Flags().Bool(flagRebuild, false,
		"Re-propose the unapproved folders with the current config.yaml rules before reviewing "+
			"(no re-scan or re-hash); already-saved time slices are kept — re-open one in review to redo it")
	return cmd
}

func (a *app) runReview(cmd *cobra.Command) error {
	rebuild, _ := cmd.Flags().GetBool(flagRebuild)
	yes, _ := cmd.Flags().GetBool(flagYes)

	// No guard on --rebuild any more: a rebuild re-proposes what nobody has
	// approved and leaves the approved plan alone (see vfs.persist), so there
	// is no confirmed work left for it to discard.
	switch {
	case yes:
		return a.confirmReviewAll(rebuild)
	case a.isTuiEnabled(cmd):
		// Opens the app on the review tab — the same session a bare
		// `wandersort` gives, so a reviewer who finds the folders wrong can fix
		// the settings and come back without relaunching. The shell opens the
		// lock, the database and the tree itself, and reports a library with
		// nothing to review on screen rather than refusing to start: there is a
		// scan tab one ctrl+t away, which is exactly what that user needs.
		return a.runShell(shellStart{tab: tabReview, rebuild: rebuild})
	default:
		// An alt-screen review in a pipe was never usable; say so instead of
		// drawing one into a file.
		return fmt.Errorf("review needs an interactive terminal — use 'wandersort review --yes' to confirm the proposal as-is")
	}
}

// confirmReviewAll is `review --yes`: no TUI, so the lock, the database and
// the proposal work all run inline here, a missing library is a hard error
// rather than a screen, and a settings change is a warning rather than a
// question — there is nobody to ask.
func (a *app) confirmReviewAll(rebuild bool) error {
	if _, err := os.Stat(a.Config.AppDBPath); os.IsNotExist(err) {
		return fmt.Errorf("no database found — run 'wandersort scan' first")
	}

	l, err := a.lockOutput()
	if err != nil {
		return err
	}
	defer l.Unlock()

	ctx := context.Background()
	if err := a.initAppDB(ctx); err != nil {
		return fmt.Errorf("app db: %w", err)
	}
	defer a.closeDBs()

	// rebuild only re-runs the vfs phase — no exif phase, so no exiftool
	// needed; ask only for what this command needs.
	a.Deps = a.newDeps(nil)
	a.Deps.StartLocationOnly(ctx, nil)

	outputDir := filepath.Dir(a.Config.AppDBPath)
	if !rebuild && a.settingsChanged(outputDir) {
		a.Log.Warn("Settings changed since this proposal — run 'wandersort review --rebuild' to apply them",
			logger.UserKey, true)
	}

	resolver, err := a.Deps.Location()
	if err != nil && !rebuild {
		a.Log.Warn("Location resolver unavailable, rename completions disabled", "error", err)
	} else if err != nil {
		return fmt.Errorf("dependencies: %w", err)
	}
	if rebuild {
		a.Log.Info("Rebuilding folder proposal", logger.UserKey, true)
		if _, err := vfs.Propose(ctx, a.AppDB, resolver, a.Config, a.Log); err != nil {
			return fmt.Errorf("rebuild proposal: %w", err)
		}
	}
	tree, err := vfs.BuildTree(ctx, a.AppDB, nil)
	if err != nil {
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
// right now and returns the new tree — the "yes" arm of the review's rebuild
// question. `a.Config` is re-resolved on every wizard save (see
// app.reloadConfig), so "right now" really is what the user last saved.
//
// Re-proposing is always library-wide (vfs.Propose replaces every unapproved
// row); seg only scopes the tree handed back, so a reset inside one time slice
// returns that slice, not the whole library.
func (a *app) rebuildTree(ctx context.Context, seg *vfs.Segment) ([]vfs.Node, error) {
	resolver, err := a.Deps.Location()
	if err != nil {
		return nil, fmt.Errorf("dependencies: %w", err)
	}
	a.Log.Info("Rebuilding folder proposal", logger.UserKey, true)
	if _, err := vfs.Propose(ctx, a.AppDB, resolver, a.Config, a.Log); err != nil {
		return nil, fmt.Errorf("rebuild proposal: %w", err)
	}
	return vfs.BuildTree(ctx, a.AppDB, seg)
}

// newReviewScreen builds the review screen over the current proposal, reusing
// the scan's already-open DB and Deps — no lock/DB re-init needed. It never
// rebuilds on the way in: the review asks about a settings change itself, on
// screen, so there is one place that decision is made.
func (a *app) newReviewScreen(ctx context.Context) (tea.Model, error) {
	// Doesn't block: a.Deps was started by the scan and vfs already ran, so
	// the location download has resolved by now. Autocomplete just degrades
	// gracefully without a resolver if it somehow hasn't.
	resolver, err := a.Deps.Location()
	if err != nil {
		a.Log.Warn("Location resolver unavailable, rename completions disabled", "error", err)
	}
	tree, err := vfs.BuildTree(ctx, a.AppDB, nil)
	if err != nil {
		return nil, err
	}
	if len(tree) == 0 {
		return nil, fmt.Errorf("no proposal to review")
	}
	return review.Screen(ctx, review.Options{
		DB:              a.AppDB,
		Tree:            tree,
		Resolver:        resolver,
		Log:             a.Log,
		OutputDir:       filepath.Dir(a.Config.AppDBPath),
		Rebuild:         a.rebuildTree,
		SettingsChanged: a.settingsChanged(filepath.Dir(a.Config.AppDBPath)),
		SegmentMonths:   a.Config.SegmentMonths,
	}), nil
}

// reportReviewOutcome reports how the embedded review ended, and hands back
// the line it logged — the shell shows it above the next folder input, since
// the session carries on past the review that produced it.
func (a *app) reportReviewOutcome(confirmed bool, saveErr error) (string, error) {
	if saveErr != nil {
		return "", fmt.Errorf("save plan: %w", saveErr)
	}
	note := "Review cancelled — nothing changed"
	if confirmed {
		note = "Folder structure approved."
	}
	a.Log.Info(note, logger.UserKey, true)
	return note, nil
}
