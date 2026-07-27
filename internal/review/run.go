// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Options is everything the review TUI needs. Resolver may be nil — rename
// autocomplete degrades gracefully without it.
type Options struct {
	DB        *db.DB
	Tree      []vfs.Node
	Resolver  *location.Resolver
	Log       logger.Logger
	OutputDir string // for the post-approve free-space check
}

// Run drives the standalone full-screen review to completion and writes the
// approved plan. Returns an error if the reviewer quit without saving.
func Run(ctx context.Context, o Options) error {
	m, err := tea.NewProgram(newModel(o.Tree, ctx, o.DB, o.Resolver, o.Log),
		tea.WithOutput(os.Stderr), tea.WithAltScreen()).Run()
	if err != nil {
		return fmt.Errorf("review ui: %w", err)
	}
	rm := m.(Model)
	defer cleanupPreviewDirs(rm.previewDirs)
	if !rm.confirmed {
		return fmt.Errorf("review cancelled — nothing changed")
	}
	for _, r := range rm.rows {
		if name := strings.TrimSpace(r.newName); name != "" {
			r.node.Name = name
		}
	}
	if err := vfs.Confirm(ctx, o.DB, rm.tree); err != nil {
		return err
	}
	// review is the last look before a plan is approved — finding out mid-move
	// that the output volume is too small is far worse than being told here
	workflow.CheckOutputSpace(ctx, o.DB, o.Log, o.OutputDir)
	return nil
}

// AcceptAll takes every suggestion without showing a TUI and confirms the
// plan (`wandersort review --yes`).
func AcceptAll(ctx context.Context, o Options) error {
	for _, it := range collectReviewable(o.Tree) {
		it.Name = it.Suggestions[0].Name
	}
	if err := vfs.Confirm(ctx, o.DB, o.Tree); err != nil {
		return err
	}
	workflow.CheckOutputSpace(ctx, o.DB, o.Log, o.OutputDir)
	return nil
}

// Screen returns the review as an app-shell screen, so a scan can swap into it
// inside its own bubbletea program. Pass the model the shell leaves behind to
// Outcome.
func Screen(ctx context.Context, o Options) tea.Model {
	m := newModel(o.Tree, ctx, o.DB, o.Resolver, o.Log)
	m.embedded = true
	return screen{
		inner:     m,
		ctx:       ctx,
		db:        o.DB,
		log:       o.Log,
		outputDir: o.OutputDir,
	}
}

// Outcome reports how an embedded review ended. ok is false when m is not a
// review screen at all.
func Outcome(m tea.Model) (confirmed bool, err error, ok bool) {
	s, ok := m.(screen)
	if !ok {
		return false, nil, false
	}
	return s.confirmed, s.finalErr, true
}
