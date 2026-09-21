// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import tea "github.com/charmbracelet/bubbletea"

// SwitchMsg hands the container a screen for another tab — the scan passing
// over the review it prefetched. It is a hand*over*, not a hand-back: a
// screen that is finished with the user says so with Leave, which is the one
// way out.
type SwitchMsg struct{ Next Tab }

// Switch is the tea.Cmd a screen returns to hand over the screen it built.
func Switch(next Tab) tea.Cmd {
	return func() tea.Msg { return SwitchMsg{Next: next} }
}
