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
	"github.com/jammutkarsh/wandersort/pkg/location"
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
		"Re-run the VFS proposal with the current config.yaml rules before reviewing (no re-scan or re-hash)")
	return cmd
}

func (a *app) runReview(cmd *cobra.Command) error {
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

	rebuild, _ := cmd.Flags().GetBool(flagRebuild)
	yes, _ := cmd.Flags().GetBool(flagYes)

	// --rebuild re-proposes from metadata already in the DB: a changed rules
	// setting applies without a re-scan or re-hash. vfs.Run wipes every entry,
	// including an already-confirmed plan — the names survive in user_labels
	// as rename completions, but dropping confirmed work needs saying out loud.
	if rebuild {
		approved, err := vfs.ApprovedCount(ctx, a.AppDB)
		if err != nil {
			return err
		}
		if approved > 0 && !yes {
			return fmt.Errorf("--rebuild would discard the confirmed plan (%d approved files).\n"+
				"The names you typed are remembered as rename completions; re-run with --yes to rebuild and confirm the new plan non-interactively", approved)
		}
	}
	// rebuild only re-runs the vfs phase — no exif phase, so no exiftool
	// needed; ask only for what this command needs.
	a.Deps = a.newDeps(nil)
	a.Deps.StartLocationOnly(ctx, nil)

	outputDir := filepath.Dir(a.Config.AppDBPath)

	// Nothing re-proposes on its own, so a settings change since this plan was
	// built is only visible here. The interactive review asks about it itself
	// (a full-screen yes/no over the tree); the non-interactive paths can't
	// ask anyone, so they say it once and carry on.
	settingsChanged := !rebuild && a.settingsChanged(outputDir)
	if settingsChanged && (yes || !a.isTuiEnabled(cmd)) {
		a.Log.Warn("Settings changed since this proposal — run 'wandersort review --rebuild' to apply them",
			logger.UserKey, true)
		settingsChanged = false
	}

	// load is the proposal work (location resolver, --rebuild's vfs.Propose,
	// vfs.BuildTree) that used to run before the terminal showed anything.
	load := func(ctx context.Context) ([]vfs.Node, *location.Resolver, error) {
		resolver, err := a.Deps.Location()
		if err != nil && !rebuild {
			a.Log.Warn("Location resolver unavailable, rename completions disabled", "error", err)
			err = nil
		} else if err != nil {
			return nil, nil, fmt.Errorf("dependencies: %w", err)
		}
		if rebuild {
			a.Log.Info("Rebuilding folder proposal", logger.UserKey, true)
			if _, err := vfs.Propose(ctx, a.AppDB, resolver, a.Config, a.Log); err != nil {
				return nil, nil, fmt.Errorf("rebuild proposal: %w", err)
			}
		}
		tree, err := vfs.BuildTree(ctx, a.AppDB)
		if err != nil {
			return nil, nil, err
		}
		if len(tree) == 0 {
			return nil, nil, fmt.Errorf("no proposal to review — run 'wandersort scan' first")
		}
		return tree, resolver, nil
	}

	if yes {
		// no TUI either way — do the load inline and confirm.
		tree, _, err := load(ctx)
		if err != nil {
			return err
		}
		if err := review.ConfirmAll(ctx, review.Options{
			DB: a.AppDB, Tree: tree, Log: a.Log, OutputDir: outputDir,
		}); err != nil {
			return err
		}
	} else {
		if err := review.Run(ctx, review.Options{
			DB: a.AppDB, Log: a.Log, OutputDir: outputDir, Load: load,
			Rebuild: a.rebuildTree, SettingsChanged: settingsChanged,
		}); err != nil {
			return err
		}
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
func (a *app) rebuildTree(ctx context.Context) ([]vfs.Node, error) {
	resolver, err := a.Deps.Location()
	if err != nil {
		return nil, fmt.Errorf("dependencies: %w", err)
	}
	a.Log.Info("Rebuilding folder proposal", logger.UserKey, true)
	if _, err := vfs.Propose(ctx, a.AppDB, resolver, a.Config, a.Log); err != nil {
		return nil, fmt.Errorf("rebuild proposal: %w", err)
	}
	return vfs.BuildTree(ctx, a.AppDB)
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
	tree, err := vfs.BuildTree(ctx, a.AppDB)
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
