// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// screen embeds the review TUI as an app-shell screen so scan can swap into
// it in the same full-screen program. Unlike Run, it finalizes in-program: on
// save it runs vfs.Confirm itself, then hands back to the shell via Switch(nil).
type screen struct {
	inner     Model
	ctx       context.Context
	db        *db.DB
	log       logger.Logger
	outputDir string

	finalizing bool
	confirmed  bool
	finalErr   error
}

// finalizeMsg reports the in-program vfs.Confirm result.
type finalizeMsg struct{ err error }

func (s screen) Init() tea.Cmd { return s.inner.Init() }

func (s screen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if s.finalizing {
		if fm, ok := msg.(finalizeMsg); ok {
			s.finalErr = fm.err
			cleanupPreviewDirs(s.inner.previewDirs)
			return s, tui.Switch(nil) // quit the shell
		}
		return s, nil
	}

	im, cmd := s.inner.Update(msg)
	s.inner = im.(Model)
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

// finalize writes the reviewed plan off the UI goroutine. Every edit is
// already on the tree itself, so there is nothing to apply first.
func (s screen) finalize() tea.Cmd {
	return func() tea.Msg {
		if err := vfs.Confirm(s.ctx, s.db, s.inner.tree); err != nil {
			return finalizeMsg{err: err}
		}
		workflow.CheckOutputSpace(s.ctx, s.db, s.log, s.outputDir)
		return finalizeMsg{}
	}
}

func (s screen) View() string {
	if s.finalizing {
		return tui.Banner("review") + "\n\n  " + tui.OK.Render("Saving your plan…")
	}
	return s.inner.View()
}
