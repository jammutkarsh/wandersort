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
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// screen embeds the review TUI as an app-shell screen so scan can swap into
// it in the same full-screen program. It finalizes in-program: on save it runs
// vfs.Confirm and the free-space check itself, then hands back to the shell via
// Switch(nil). There is no non-hosted variant — the shell is the only host.
type screen struct {
	inner     Model
	ctx       context.Context
	db        *db.DB
	log       logger.Logger
	outputDir string
	// seg scopes the approval to one time slice; nil approves the whole tree.
	seg *vfs.Segment
	// host is the segment picker this screen was opened from — a snapshot, so
	// it also carries how many slices were already saved when it opened. Nil
	// for an unsegmented review, which hands straight back to its caller.
	host *pickerModel

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
			// The plan is written: the copies nobody is going to peek at again
			// are the whole cache, this session's and any earlier one's.
			if fm.err == nil {
				if err := CleanPreviews(); err != nil {
					s.log.Warn("could not remove the preview copies", "error", err)
				}
			}
			// One slice saved is not the end of a segmented review: go back to
			// the picker for the next one.
			if s.host != nil && fm.err == nil {
				next, err := s.host.reenter()
				if err == nil {
					return s, tui.Switch(next)
				}
				s.finalErr = err
			}
			return s, tui.Switch(nil) // hand back to the host
		}
		return s, nil
	}

	im, cmd := s.inner.Update(msg)
	s.inner = im.(Model)
	if !s.inner.done {
		return s, cmd
	}
	if !s.inner.confirmed {
		// Preview copies deliberately survive an unsaved exit — the next
		// session reuses them instead of re-copying the same bytes.
		// [esc] out of one slice is a step back to the picker, not the end of the
		// review — the other slices are still waiting to be looked at.
		if s.inner.back && s.host != nil {
			return s, tui.Switch(*s.host)
		}
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
		if err := vfs.Confirm(s.ctx, s.db, s.inner.tree, s.seg); err != nil {
			return finalizeMsg{err: err}
		}
		volume.CheckOutputSpace(s.ctx, s.db, s.log, s.outputDir)
		return finalizeMsg{}
	}
}

func (s screen) View() string {
	if s.finalizing {
		return tui.Banner("review") + "\n\n  " + tui.OK.Render("Saving your plan…")
	}
	return s.inner.View()
}
