package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/internal/review"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *app) newOrganiseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "organise",
		Short: "Correct the proposed folder structure before anything moves",
		Long: `Walks the folder hierarchy proposed by the last scan so you can rename,
merge, drop and flatten folders before anything is moved. Every edit is kept
as you make it, across sessions; 'wandersort execute' applies them and copies
the files. Names you type are remembered and offered as rename completions in
later reviews.`,
		Example: `# Correct the plan interactively
wandersort organise`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// an alt-screen review can't draw into a pipe
			if !a.isTuiEnabled(cmd) {
				return fmt.Errorf("organise needs an interactive terminal — 'wandersort execute' copies the plan as proposed")
			}
			// the shell on the Organise tab, so settings stay one ctrl+t away
			return a.runShell(shellStart{tab: tabReview})
		},
	}
}

// rebuildTree re-proposes the whole hierarchy under the current settings
// (after a wizard save) and returns the new tree. Placed and failed rows keep
// theirs.
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

// newReviewScreen builds the review over the current proposal using the open
// DB. An empty tree means everything is already placed.
func (a *app) newReviewScreen(ctx context.Context) (tui.Tab, error) {
	// vfs already waited for the location download; without a resolver,
	// rename completion is just disabled
	resolver, err := a.Deps.Location()
	if err != nil {
		a.Log.Warn("Location resolver unavailable, rename completions disabled", "error", err)
	}
	resolver = resolver.WithAnchors(resolver.BuildAnchors(ctx, a.Config.SavedPlaces))
	outputDir := a.Config.OutputDir()
	tree, err := vfs.BuildTree(ctx, a.AppDB)
	if err != nil {
		return nil, err
	}
	if len(tree) == 0 {
		return nil, fmt.Errorf("everything here is already organized — nothing left to review")
	}
	// a re-plan above already dropped the draft along with the old folder IDs
	draft, err := vfs.OpenDraft(outputDir, tree)
	if err != nil {
		return nil, err
	}
	return review.Screen(ctx, review.Options{
		DB:       a.AppDB,
		Draft:    draft,
		Resolver: resolver,
		Log:      a.Log,
	}), nil
}
