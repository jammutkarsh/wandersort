// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// treeLoadedMsg reports Options.Load finished off the UI goroutine. Resolver
// rides along with the tree rather than coming from Options up front: Load is
// what waits out a still-downloading location database, so the resolver
// isn't known until it returns.
type treeLoadedMsg struct {
	tree     []vfs.Node
	resolver *location.Resolver
	err      error
}

// loadingScreen is what Run shows the instant the program starts, instead of
// blocking the terminal on Options.Load (the location resolver, --rebuild's
// vfs.Propose, then vfs.BuildTree) before the TUI ever appears.
type loadingScreen struct {
	ctx  context.Context
	load func(context.Context) ([]vfs.Node, *location.Resolver, error)
	db   *db.DB
	log  logger.Logger
	spin spinner.Model
	w, h int
	err  error
}

func newLoadingScreen(ctx context.Context, o Options) loadingScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(tui.Primary)
	return loadingScreen{ctx: ctx, load: o.Load, db: o.DB, log: o.Log, spin: sp}
}

func (m loadingScreen) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, func() tea.Msg {
		tree, resolver, err := m.load(m.ctx)
		return treeLoadedMsg{tree: tree, resolver: resolver, err: err}
	})
}

func (m loadingScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.err != nil || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	case treeLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		return m, tui.Switch(newModel(msg.tree, m.ctx, m.db, msg.resolver, m.log))
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m loadingScreen) View() string {
	top := tui.Banner("review")
	if m.err != nil {
		body := top + "\n" + tui.Bad.Render("Could not open review: ") + tui.Text.Render(m.err.Error())
		return tui.Screen(body, tui.Footer(tui.KeyHint("q", "quit"), m.w), m.h)
	}
	body := top + "\n" + m.spin.View() + " " + tui.DimText.Render("Loading proposal…")
	return tui.Screen(body, "", m.h)
}
