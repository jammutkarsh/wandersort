// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"fmt"

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

// rebuildTree re-proposes the whole hierarchy from the settings as they stand
// right now and returns the new tree — called when a wizard save changes
// something, which is the only way the settings can move under a plan now
// that they live in the library's own database (see shell.configSaved).
//
// Every row not yet transferred is re-proposed, so a re-plan really does
// replan everything; a file already placed or failed keeps its row.
func (a *app) rebuildTree(ctx context.Context) ([]vfs.Node, error) {
	resolver, err := a.Deps.Location()
	if err != nil {
		return nil, fmt.Errorf("dependencies: %w", err)
	}
	a.Log.Info("Settings changed — re-proposing the folder structure", logger.UserKey, true)
	if _, err := vfs.Propose(ctx, a.AppDB, resolver, a.Config, a.Log); err != nil {
		return nil, fmt.Errorf("re-plan proposal: %w", err)
	}
	return vfs.BuildTree(ctx, a.AppDB)
}

// newReviewScreen builds the review screen over the current proposal, reusing
// the scan's already-open DB and Deps — no lock/DB re-init needed. The plan
// it finds always matches the current settings: a save re-plans on the spot
// (shell.configSaved), so there is nothing stale to check for here.
//
// An empty tree means every master is already placed by an earlier execute —
// a fully organized library, not a plan to rebuild.
func (a *app) newReviewScreen(ctx context.Context) (tea.Model, error) {
	// Doesn't block: a.Deps was started by the scan and vfs already ran, so
	// the location download has resolved by now. Autocomplete just degrades
	// gracefully without a resolver if it somehow hasn't.
	resolver, err := a.Deps.Location()
	if err != nil {
		a.Log.Warn("Location resolver unavailable, rename completions disabled", "error", err)
	}
	outputDir := a.Config.OutputDir()
	tree, err := vfs.BuildTree(ctx, a.AppDB)
	if err != nil {
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
