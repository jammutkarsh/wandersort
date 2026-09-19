// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"fmt"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/internal/review"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func (a *app) newReviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "review",
		Short: "Review and correct the proposed folder structure",
		Long: `Walks the folder hierarchy proposed by the last scan so you can rename,
merge, drop and flatten folders before anything is moved. Every edit is kept
as you make it, across sessions; 'wandersort execute' applies them and copies
the files. Names you type are remembered and offered as rename completions in
later reviews.`,
		Example: `# Review interactively
wandersort review`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// An alt-screen review in a pipe was never usable; say so instead
			// of drawing one into a file.
			if !a.isTuiEnabled(cmd) {
				return fmt.Errorf("review needs an interactive terminal — 'wandersort execute' copies the plan as proposed")
			}
			// Opens the app on the review tab — the same session a bare
			// `wandersort` gives, so a reviewer who finds the folders wrong can
			// fix the settings and come back without relaunching. The shell
			// opens the lock, the database and the tree itself, and reports a
			// library with nothing to review on screen rather than refusing to
			// start: there is a scan tab one ctrl+t away.
			return a.runShell(shellStart{tab: tabReview})
		},
	}
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
// stale stamp, or a wizard save that changes
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
	// a re-plan above already dropped the draft along with the old folder IDs
	edits, err := vfs.ReadDraft(outputDir)
	if err != nil {
		return nil, err
	}
	return review.Screen(ctx, review.Options{
		DB:        a.AppDB,
		Tree:      tree,
		Edits:     edits,
		Resolver:  resolver,
		Log:       a.Log,
		OutputDir: outputDir,
	}), nil
}
