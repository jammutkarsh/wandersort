// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// InstallProgressMsg carries dependency-download byte progress into the setup
// install screen. It comes straight from a callback (not the logger), so the
// per-byte ticks never touch the file log.
type InstallProgressMsg struct {
	Phase string
	Done  int64
	Total int64
}

type installDoneMsg struct{ err error }

// InstallModel is the full-screen dependency-install view shown before the
// setup wizard: a StageList stage per dependency (exiftool, location DB) with
// a byte progress bar. Cached dependencies flip straight to done, so a re-run
// flashes briefly and moves on.
type InstallModel struct {
	work func() error
	sl   StageList
	w, h int
	err  error

	// Next, when set, builds the screen to swap to once installs succeed (the
	// setup wizard) — keeping install→wizard in one alt-screen program so there's
	// no terminal-restore flash between them. nil means this screen just quits.
	Next func() (tea.Model, error)
}

// NewInstallModel builds the install screen. work runs the actual installs
// (exiftool + location DB) and blocks; its start/done milestones must arrive as
// LogEventMsg and its byte progress as InstallProgressMsg.
func NewInstallModel(work func() error) InstallModel {
	sl := NewStageList(
		func(cur, total int) string {
			return fmt.Sprintf("%s / %s", humanBytes(cur), humanBytes(total))
		},
		&Stage{Key: "exiftool", Name: "Exiftool", HasBar: true},
		&Stage{Key: "location", Name: "Location DB", HasBar: true},
	)
	return InstallModel{work: work, sl: sl}
}

// Err reports why the install failed (nil on success), read after the program exits.
func (m InstallModel) Err() error { return m.err }

func (m InstallModel) Init() tea.Cmd {
	return tea.Batch(m.sl.Init(), func() tea.Msg {
		return installDoneMsg{err: m.work()}
	})
}

func (m InstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.sl.SetWidth(msg.Width)
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	case InstallProgressMsg:
		return m, m.sl.SetProgress(msg.Phase, int(msg.Done), int(msg.Total))
	case LogEventMsg:
		if p, ok := msg.Event.Attrs[logger.PhaseKey].(string); ok {
			switch msg.Event.Attrs[logger.EventKey] {
			case "start":
				m.sl.Start(p, msg.Event.Message)
			case "done":
				m.sl.Done(p, "done", "")
			}
		}
		return m, nil
	case installDoneMsg:
		m.err = msg.err
		m.sl.FinishRemaining(msg.err != nil, "ready")
		if msg.err == nil && m.Next != nil {
			next, err := m.Next()
			if err != nil {
				m.err = err
				return m, tea.Quit
			}
			return m, Switch(next) // swap in-place, no alt-screen flash
		}
		return m, tea.Quit
	}
	return m, m.sl.Update(msg)
}

func (m InstallModel) View() string {
	body := Banner("setup · dependencies") + "\n\n" + m.sl.View(m.w, 0)
	return Screen(body, Footer(KeyHint("ctrl+c", "cancel"), m.w), m.h)
}

// humanBytes renders a byte count as a compact human string (KB/MB).
func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
