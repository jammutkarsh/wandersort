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
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// reviewScreen embeds the review TUI as an app-shell screen so scan can swap
// straight into it in the same full-screen program. Unlike standalone review
// (runReview), it finalizes in-program: on save it runs vfs.Confirm itself,
// then hands control back to the shell (Switch(nil) quits the program). The
// caller reads confirmed/finalErr afterwards to print the outcome.
type reviewScreen struct {
	inner     reviewModel
	ctx       context.Context
	sessionID uuid.UUID
	db        *db.DB
	log       logger.Logger
	outputDir string

	finalizing bool
	confirmed  bool
	finalErr   error
}

// reviewFinalizeMsg reports the in-program vfs.Confirm result.
type reviewFinalizeMsg struct{ err error }

// newReviewScreen builds the review screen over the current proposal, reusing
// the already-open DB and resolver from the scan (no lock/DB re-init — the
// caller still holds the output lock). Returns vfs.ErrNoProposal's friendly
// form if there is nothing to review.
func (a *App) newReviewScreen(ctx context.Context) (tea.Model, error) {
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
	if err := a.InitLocationResolver(ctx); err != nil {
		a.Log.Warn("Location resolver unavailable, rename suggestions disabled", "error", err)
	}

	m := newReviewModel(tree, ctx, a.AppDB, sessionID, a.LocationResolver, a.Log)
	m.embedded = true
	return reviewScreen{
		inner:     m,
		ctx:       ctx,
		sessionID: sessionID,
		db:        a.AppDB,
		log:       a.Log,
		outputDir: filepath.Dir(a.Config.AppDBPath),
	}, nil
}

func (s reviewScreen) Init() tea.Cmd { return s.inner.Init() }

func (s reviewScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if s.finalizing {
		if fm, ok := msg.(reviewFinalizeMsg); ok {
			s.finalErr = fm.err
			cleanupPreviewDirs(s.inner.previewDirs)
			return s, tui.Switch(nil) // quit the shell
		}
		return s, nil
	}

	im, cmd := s.inner.Update(msg)
	s.inner = im.(reviewModel)
	if !s.inner.done {
		return s, cmd
	}
	if !s.inner.confirmed {
		cleanupPreviewDirs(s.inner.previewDirs)
		return s, tui.Switch(nil)
	}
	s.confirmed = true
	s.finalizing = true
	return s, s.finalize()
}

// finalize applies the reviewer's renames and writes the plan, off the UI
// goroutine. The nodes are pointers into inner.tree, so the goroutine's writes
// are visible to Confirm.
func (s reviewScreen) finalize() tea.Cmd {
	return func() tea.Msg {
		for _, r := range s.inner.rows {
			if name := strings.TrimSpace(r.newName); name != "" {
				r.node.Name = name
			}
		}
		if err := vfs.Confirm(s.ctx, s.sessionID, s.db, s.inner.tree); err != nil {
			return reviewFinalizeMsg{err: err}
		}
		workflow.CheckOutputSpace(s.ctx, s.db, s.log, s.outputDir, s.sessionID)
		return reviewFinalizeMsg{}
	}
}

func (s reviewScreen) View() string {
	if s.finalizing {
		return tui.Banner("review") + "\n\n  " + tui.OK.Render("Saving your plan…")
	}
	return s.inner.View()
}
