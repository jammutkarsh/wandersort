package style

import "github.com/charmbracelet/lipgloss"

// Shared ANSI 256 colors.
const (
	colorError   = lipgloss.Color("1") // red
	colorSuccess = lipgloss.Color("2") // green
	colorWarn    = lipgloss.Color("3") // yellow
	colorAccent  = lipgloss.Color("6") // cyan
	colorDim     = lipgloss.Color("8") // gray
	colorMuted   = lipgloss.Color("250")
)

var (
	Err     = lipgloss.NewStyle().Foreground(colorError).Bold(true)
	Success = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	Warn    = lipgloss.NewStyle().Foreground(colorWarn)
	Header  = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	Dim     = lipgloss.NewStyle().Foreground(colorDim)
	Desc    = lipgloss.NewStyle().Foreground(colorMuted)
)
