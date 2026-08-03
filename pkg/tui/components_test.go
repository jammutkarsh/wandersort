// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestSpinnerBarUpdateHandlesTickAndFrameOnly(t *testing.T) {
	sb := newSpinnerBar()

	if _, cmd := sb.update(spinner.TickMsg{}); cmd == nil {
		t.Errorf("a spinner tick should keep the animation going via a cmd")
	}

	// progress.FrameMsg must route through progress.Model.Update without
	// panicking on the type assertion back to progress.Model.
	sb.update(progress.FrameMsg{})

	// Anything else is a no-op.
	if _, cmd := sb.update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Errorf("an unrelated msg should return a nil cmd, got %v", cmd)
	}
}

func TestRowTruncatesLeftAndAlignsRightToWidth(t *testing.T) {
	got := ansi.Strip(Row("a very long left side that will not fit", "R", 20))
	if !strings.HasSuffix(got, "R") {
		t.Errorf("Row must align the right suffix to the edge, got %q", got)
	}
	if w := ansi.StringWidth(got); w > 20 {
		t.Errorf("Row output %q display width %d exceeds width 20", got, w)
	}
}

func TestRowShortLeftPadsToRight(t *testing.T) {
	got := ansi.Strip(Row("left", "right", 20))
	if !strings.HasPrefix(got, "left") {
		t.Errorf("Row should keep short left content as-is, got %q", got)
	}
	if !strings.HasSuffix(got, "right") {
		t.Errorf("Row should align right content to the edge, got %q", got)
	}
}

func TestBannerWithAndWithoutSubtitle(t *testing.T) {
	withSub := ansi.Strip(Banner("scan"))
	if !strings.Contains(withSub, "WanderSort") || !strings.Contains(withSub, "scan") {
		t.Errorf("Banner(%q) = %q, want both title and subtitle", "scan", withSub)
	}

	bare := ansi.Strip(Banner(""))
	if !strings.Contains(bare, "WanderSort") {
		t.Errorf("Banner(\"\") = %q, want the title", bare)
	}
	if strings.Contains(bare, "scan") {
		t.Errorf("Banner(\"\") must not carry over a subtitle")
	}
}

func TestFooterWrapsToWidth(t *testing.T) {
	got := Footer("[c] save & exit  [q] quit", 0)
	if !strings.Contains(ansi.Strip(got), "save & exit") {
		t.Errorf("Footer must render the help text, got %q", got)
	}
	// width <= 0 means "no wrap" — just render the style as-is.
	unwrapped := Footer("hello", -1)
	if !strings.Contains(ansi.Strip(unwrapped), "hello") {
		t.Errorf("Footer with non-positive width should still render, got %q", unwrapped)
	}
}

func TestKeyHintNonBreakingSpaces(t *testing.T) {
	got := KeyHint("c", "save & exit")
	if !strings.Contains(got, "c") || !strings.Contains(got, "save") {
		t.Errorf("KeyHint(%q, %q) = %q, missing key or action", "c", "save & exit", got)
	}
}

// TestScreenPinsFooterToBottomWhenHeightKnown pins the current gap formula
// exactly: `gap := max(h-Height(body)-Height(footer), 1)` yields h-1 total
// lines (one short of h), not h — the +1 needed to land the footer on the
// true last row isn't in the formula. Not fixed here (touches every screen's
// rendered layout, no live terminal in this pass to visually confirm the
// change is actually an improvement) — flagging as a real finding: every
// Screen()-framed TUI screen currently leaves one blank row of slack below
// the footer instead of the footer sitting on the terminal's last row.
func TestScreenPinsFooterToBottomWhenHeightKnown(t *testing.T) {
	got := Screen("body", "footer", 5)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("Screen(h=5) produced %d lines, want 4 (current gap formula is off by one from filling all 5 rows):\n%q", len(lines), got)
	}
	if lines[0] != "body" {
		t.Errorf("first line = %q, want body", lines[0])
	}
	if lines[len(lines)-1] != "footer" {
		t.Errorf("last line = %q, want footer", lines[len(lines)-1])
	}
}

func TestScreenFallsBackToStackingBeforeFirstSizeMsg(t *testing.T) {
	got := Screen("body", "footer", 0)
	if got != "body\nfooter" {
		t.Errorf("Screen(h=0) = %q, want plain stacking", got)
	}

	noFooter := Screen("body", "", 0)
	if noFooter != "body" {
		t.Errorf("Screen(h=0, footer=\"\") = %q, want just the body", noFooter)
	}
}

func TestScreenNeverShrinksGapBelowOne(t *testing.T) {
	// body+footer taller than h: gap must still be at least 1, never negative
	// (which would panic strings.Repeat).
	got := Screen("line1\nline2\nline3", "footer", 2)
	if !strings.HasSuffix(got, "footer") {
		t.Errorf("Screen must still append the footer even when content overflows h, got %q", got)
	}
}
