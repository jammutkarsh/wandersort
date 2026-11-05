// Package tui is WanderSort's terminal design system: palette, reusable
// bubbletea/lipgloss components, and the app-shell that swaps screens.
// See README.md in this package for the design rules.
package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Palette — adaptive truecolor. Each colour resolves to a tuned hex for the
// terminal's light or dark background, so the UI reads well either way.
var (
	Primary   = lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#A78BFA"} // violet — brand / focus
	Success   = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#34D399"} // green — done / yes
	Warn      = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"} // amber — warnings
	Error     = lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#F87171"} // red — failures
	Muted     = lipgloss.AdaptiveColor{Light: "#4B5563", Dark: "#D1D5DB"} // gray — secondary text
	Subtle    = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"} // dim — hints, pending rows (still 4.5:1 text)
	Border    = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#4B5563"} // lines only, never text
	Fg        = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#E5E7EB"} // primary text
	Highlight = lipgloss.AdaptiveColor{Light: "#DDD6FE", Dark: "#4F3F8C"} // selected-row background
	Folder    = lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#60A5FA"} // blue — a folder the copy creates
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
	// NewFolder names a folder not yet in the library, blue like a folder
	// icon on every desktop.
	NewFolder = lipgloss.NewStyle().Foreground(Folder)

	Box = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Border).
		Padding(0, 2)
)
