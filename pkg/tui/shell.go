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
