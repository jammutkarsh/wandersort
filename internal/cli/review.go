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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/internal/review"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func (a *app) newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Review and correct the proposed folder structure",
		Long: `Walks the folder hierarchy proposed by the last scan so you can rename
directories the pipeline could not resolve confidently (for example an
unlocated event cluster) before anything is moved. Confirmed names are
remembered and suggested automatically on future scans.`,
		Example: `# Review interactively
wandersort review

# Skip the TUI: accept every suggestion and confirm the plan immediately
wandersort review --yes

# Re-propose the hierarchy with the current config.yaml rules first
wandersort review --rebuild`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReview()
		},
	}

	cmd.Flags().Bool(flagYes, false, "Skip the interactive review: accept every suggestion and confirm the plan immediately")
	cmd.Flags().Bool(flagRebuild, false,
		"Re-run the VFS proposal with the current config.yaml rules before reviewing (no re-scan or re-hash)")
	return cmd
}

func (a *app) runReview() error {
	if _, err := os.Stat(a.Config.AppDBPath); os.IsNotExist(err) {
		return fmt.Errorf("no database found — run 'wandersort scan' first")
	}

	l, err := lock.AcquireOutput(filepath.Dir(a.Config.LogFile))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer l.Unlock()

	ctx := context.Background()
	if err := a.initAppDB(ctx); err != nil {
		return fmt.Errorf("app db: %w", err)
	}
	defer a.closeDBs()

	sessionID, err := vfs.ProposalSession(ctx, a.AppDB)
	if err != nil {
		if errors.Is(err, vfs.ErrNoProposal) {
			return fmt.Errorf("no proposal to review — run 'wandersort scan' first")
		}
		return err
	}

	// --rebuild re-proposes from metadata already in the DB: a changed rules
	// setting applies without a re-scan or re-hash
	if v.GetBool(flagRebuild) {
		// vfs.Run wipes every entry, including an already-confirmed plan.
		// Names survive in user_labels and come back as suggestions, but
		// dropping confirmed work needs saying out loud.
		approved, err := approvedCount(ctx, a.AppDB, sessionID)
		if err != nil {
			return err
		}
		if approved > 0 && !v.GetBool(flagYes) {
			return fmt.Errorf("--rebuild would discard the confirmed plan for this session (%d approved files).\n"+
				"Your confirmed folder names are remembered and will be re-suggested; re-run with --yes to rebuild and confirm the new plan non-interactively", approved)
		}
		// EnsureDependencies already opens the resolver (and holds the install
		// lock while it does), so no separate initLocationResolver here.
		if err := a.ensureDependencies(ctx, nil); err != nil {
			return fmt.Errorf("dependencies: %w", err)
		}
		cfg := vfs.ConfigFor(a.Config)
		a.Log.Info("Rebuilding folder proposal", "rules", cfg.Rules, logger.UserKey, true)
		if _, err := vfs.New(a.AppDB, a.LocationResolver, a.Log, cfg).Run(ctx, sessionID); err != nil {
			return fmt.Errorf("rebuild proposal: %w", err)
		}
	}

	tree, err := vfs.BuildTree(ctx, sessionID, a.AppDB)
	if err != nil {
		return err
	}
	if len(tree) == 0 {
		return fmt.Errorf("no proposal to review — run 'wandersort scan' first")
	}

	opts := review.Options{
		DB:        a.AppDB,
		SessionID: sessionID,
		Tree:      tree,
		Log:       a.Log,
		OutputDir: filepath.Dir(a.Config.AppDBPath),
	}

	if v.GetBool(flagYes) {
		if err := review.AcceptAll(ctx, opts); err != nil {
			return err
		}
	} else {
		// rename autocomplete degrades gracefully without a resolver
		if err := a.initLocationResolver(ctx); err != nil {
			a.Log.Warn("Location resolver unavailable, rename suggestions disabled", "error", err)
		}
		opts.Resolver = a.LocationResolver
		if err := review.Run(ctx, opts); err != nil {
			return err
		}
	}

	fmt.Fprintln(os.Stderr, "Folder structure approved.")
	return nil
}

// approvedCount is how many of the session's entries the user already
// confirmed — the size of the plan a --rebuild would throw away.
func approvedCount(ctx context.Context, database *db.DB, sessionID uuid.UUID) (int, error) {
	var n int
	if err := database.SQL.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM virtual_fs_entries WHERE session_id = ? AND status = ?`,
		sessionID.String(), db.StatusApproved); err != nil {
		return 0, fmt.Errorf("count approved entries: %w", err)
	}
	return n, nil
}

// newReviewScreen builds the review screen over the current proposal, reusing
// the already-open DB and resolver from the scan (no lock/DB re-init — the
// caller still holds the output lock).
func (a *app) newReviewScreen(ctx context.Context) (tea.Model, error) {
	sessionID, err := vfs.ProposalSession(ctx, a.AppDB)
	if err != nil {
		if errors.Is(err, vfs.ErrNoProposal) {
			return nil, fmt.Errorf("no proposal to review")
		}
		return nil, err
	}
	tree, err := vfs.BuildTree(ctx, sessionID, a.AppDB)
	if err != nil {
		return nil, err
	}
	if len(tree) == 0 {
		return nil, fmt.Errorf("no proposal to review")
	}
	// Rename autocomplete degrades gracefully without a resolver.
	if err := a.initLocationResolver(ctx); err != nil {
		a.Log.Warn("Location resolver unavailable, rename suggestions disabled", "error", err)
	}
	return review.Screen(ctx, review.Options{
		DB:        a.AppDB,
		SessionID: sessionID,
		Tree:      tree,
		Resolver:  a.LocationResolver,
		Log:       a.Log,
		OutputDir: filepath.Dir(a.Config.AppDBPath),
	}), nil
}

// reportReviewOutcome reports how the embedded review ended
func (a *app) reportReviewOutcome(confirmed bool, saveErr error) error {
	switch {
	case saveErr != nil:
		return fmt.Errorf("save plan: %w", saveErr)
	case !confirmed:
		a.Log.Info("Review cancelled — nothing changed", logger.UserKey, true)
		return nil
	default:
		a.Log.Info("Folder structure approved.", logger.UserKey, true)
		return nil
	}
}
