// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import tea "github.com/charmbracelet/bubbletea"

// SwitchMsg asks the shell to replace the active screen with Next (e.g. scan →
// review). A screen returns it from Update via a tea.Cmd. Next==nil quits.
type SwitchMsg struct{ Next tea.Model }

// Switch is the tea.Cmd a screen returns to hand control to the next screen.
func Switch(next tea.Model) tea.Cmd {
	return func() tea.Msg { return SwitchMsg{Next: next} }
}

// Shell is the root full-screen model. It hosts one screen at a time, forwards
// window-size and every other message to it, and swaps screens on SwitchMsg —
// re-seeding the new screen's Init and current size so it renders immediately.
type Shell struct {
	cur  tea.Model
	w, h int
}

func NewShell(first tea.Model) Shell { return Shell{cur: first} }

func (s Shell) Init() tea.Cmd { return s.cur.Init() }

func (s Shell) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = m.Width, m.Height
	case SwitchMsg:
		if m.Next == nil {
			return s, tea.Quit
		}
		s.cur = m.Next
		// Init the new screen and immediately hand it the current size so it
		// lays out on the first frame instead of after the next resize.
		return s, tea.Batch(s.cur.Init(), func() tea.Msg {
			return tea.WindowSizeMsg{Width: s.w, Height: s.h}
		})
	}
	var cmd tea.Cmd
	s.cur, cmd = s.cur.Update(msg)
	return s, cmd
}

func (s Shell) View() string { return s.cur.View() }

// Current returns the active screen — the caller reads its final state after
// the program exits (e.g. whether the review screen confirmed).
func (s Shell) Current() tea.Model { return s.cur }
