package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/internal/review"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *app) newOrganiseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "organise",
		Short: "Correct the proposed folder structure before anything is copied",
		Long: `Walks the folder hierarchy proposed by the last scan so you can rename,
merge, drop and flatten folders before anything is copied. Every edit is kept
as you make it, across sessions; 'wandersort copy' applies them and copies
the files. Names you type are remembered and offered as rename completions in
later reviews.`,
		Example: `# Correct the plan interactively
wandersort organise`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// an alt-screen review can't draw into a pipe
			if !a.isTuiEnabled(cmd) {
				return fmt.Errorf("organise needs a terminal; 'wandersort copy' copies the plan as is")
			}
			// the shell on the Organise tab, so settings stay one shift+tab away
			return a.runShell(shellStart{tab: tabReview})
		},
	}
}

// newReviewScreen builds the review over the current proposal; an empty plan
// is vfs.ErrNothingNew.
func (a *app) newReviewScreen(ctx context.Context) (tui.Tab, error) {
	r, err := a.Review(ctx, a.Deps.Location)
	if err != nil {
		return nil, err
	}
	return review.Screen(ctx, review.Options{Review: r, Log: a.Log()}), nil
}
