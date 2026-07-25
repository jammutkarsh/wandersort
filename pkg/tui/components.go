// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// spinnerBar bundles the spinner + progress bar every stage-list screen
// (scan, install) needs, plus the tea.Msg plumbing to drive them — shared so
// neither screen hand-rolls the same setup and Update cases.
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

// Banner renders the branded title bar shown at the top of every screen:
// "WanderSort" + a subtitle (e.g. "scan", "setup", "review").
func Banner(subtitle string) string {
	title := Title.Render("WanderSort")
	if subtitle != "" {
		return Box.Render(title + "  " + DimText.Render(subtitle))
	}
	return Box.Render(title)
}

// Footer renders a dim, width-wrapped key-help bar. Wrapping to the terminal
// width (rather than a fixed line) is what keeps the bottom of a screen from
// being pushed off on a narrow terminal — the caller measures its height with
// lipgloss.Height and budgets the body accordingly.
func Footer(help string, width int) string {
	s := FaintTxt
	if width > 0 {
		s = s.Width(width)
	}
	return s.Render(help)
}

// Row lays out one full-width line: left content truncated to fit, right
// suffix aligned to the terminal edge — the same shape every screen's rows
// use (elapsed time on scan, file counts on review).
func Row(left, right string, width int) string { return row(left, right, width) }

// KeyHint styles a "[key] action" hint pair for footers. All spaces inside the
// pair are non-breaking, so a width-wrapped footer breaks between hints, never
// through one — "c save & exit" wrapping after the "c" reads as a stray key.
func KeyHint(key, action string) string {
	return lipgloss.NewStyle().Foreground(Primary).Render(key) + " " +
		FaintTxt.Render(strings.ReplaceAll(action, " ", " "))
}

// Screen frames a full-height view: body at the top, footer pinned to the
// bottom of the terminal, so short content (a two-line install list, a handful
// of stage rows) doesn't leave the footer floating mid-screen with dead space
// below it. h<=0 (before the first size msg) falls back to plain stacking.
func Screen(body, footer string, h int) string {
	body = strings.TrimRight(body, "\n")
	if h <= 0 {
		if footer == "" {
			return body
		}
		return body + "\n" + footer
	}
	gap := max(h-lipgloss.Height(body)-lipgloss.Height(footer), 1)
	return body + strings.Repeat("\n", gap) + footer
}
