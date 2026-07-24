// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tui is WanderSort's terminal design system: one adaptive truecolor
// palette, reusable bubbletea/lipgloss components (Docker-style stage list,
// footer, banner), custom form components, and the app-shell that swaps
// between screens. Everything visual lives here — full-screen TUI and plain
// line output alike — so every command reads as one product. See README.md
// in this package for the design rules.
package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Palette — adaptive truecolor. Each colour resolves to a tuned hex for the
// terminal's light or dark background, so the UI reads well either way.
var (
	Primary   = lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#A78BFA"} // violet — brand / focus
	Success   = lipgloss.AdaptiveColor{Light: "#059669", Dark: "#34D399"} // green — done / yes
	Warn      = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"} // amber — warnings
	Error     = lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#F87171"} // red — failures
	Muted     = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"} // gray — secondary text
	Subtle    = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#4B5563"} // dim — borders, pending
	Fg        = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#E5E7EB"} // primary text
	Highlight = lipgloss.AdaptiveColor{Light: "#EDE9FE", Dark: "#3B2F63"} // selected-row background
	OnPrimary = lipgloss.Color("15")                                      // text on a Primary fill
)

// Semantic styles shared by every screen.
var (
	Title    = lipgloss.NewStyle().Foreground(Primary).Bold(true)
	Text     = lipgloss.NewStyle().Foreground(Fg)
	DimText  = lipgloss.NewStyle().Foreground(Muted)
	FaintTxt = lipgloss.NewStyle().Foreground(Subtle)
	OK       = lipgloss.NewStyle().Foreground(Success).Bold(true)
	Attn     = lipgloss.NewStyle().Foreground(Warn)
	Bad      = lipgloss.NewStyle().Foreground(Error).Bold(true)
	Selected = lipgloss.NewStyle().Background(Highlight).Foreground(Fg)

	Box = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Subtle).
		Padding(0, 2)
)
