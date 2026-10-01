package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// spinnerBar bundles the spinner and progress bar every stage-list screen uses.
type spinnerBar struct {
	spin spinner.Model
	bar  progress.Model
}

func newSpinnerBar() spinnerBar {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(Primary)
	return spinnerBar{
		spin: sp,
		bar:  progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage()),
	}
}

// update handles spinner.TickMsg/progress.FrameMsg, the two messages that
// keep the spinner and bar animating; any other msg is a no-op.
func (sb spinnerBar) update(msg tea.Msg) (spinnerBar, tea.Cmd) {
	switch msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		sb.spin, cmd = sb.spin.Update(msg)
		return sb, cmd
	case progress.FrameMsg:
		pm, cmd := sb.bar.Update(msg)
		sb.bar = pm.(progress.Model)
		return sb, cmd
	}
	return sb, nil
}

// Brand is the app's name as the first thing on every screen's top line.
func Brand() string { return Title.Render("◆ wandersort") }

// Footer renders a dim key-help bar wrapped to width; callers measure its
// height with lipgloss.Height.
func Footer(help string, width int) string {
	s := FaintTxt
	if width > 0 {
		s = s.Width(width)
	}
	return s.Render(help)
}

// Row lays out one full-width line: left truncated to fit, right aligned to
// the terminal edge.
func Row(left, right string, width int) string { return row(left, right, width) }

// KeyHint styles a "[key] action" pair with non-breaking spaces, so a wrapped
// footer breaks between hints, never inside one.
func KeyHint(key, action string) string {
	return lipgloss.NewStyle().Foreground(Primary).Render(key) + " " +
		FaintTxt.Render(strings.ReplaceAll(action, " ", " "))
}

// Screen frames a full-height view with the footer pinned to the bottom row.
func Screen(body, footer string, h int) string {
	body = strings.TrimRight(body, "\n")
	if h <= 0 { // before the first size msg — fall back to plain stacking
		if footer == "" {
			return body
		}
		return body + "\n" + footer
	}
	gap := max(h-lipgloss.Height(body)-lipgloss.Height(footer), 1)
	return body + strings.Repeat("\n", gap) + footer
}
