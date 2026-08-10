// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// stubScreen is a minimal tea.Model that records what it was sent, standing
// in for a real screen the scan hands control to.
type stubScreen struct {
	view    string
	initCmd tea.Cmd
	msgs    []tea.Msg
}

func (s *stubScreen) Init() tea.Cmd { return s.initCmd }

func (s *stubScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	s.msgs = append(s.msgs, msg)
	return s, nil
}

func (s *stubScreen) View() string { return s.view }

// switchTarget returns the model a cmd switches to, or nil.
func switchTarget(cmd tea.Cmd) tea.Model {
	for _, msg := range flattenCmd(cmd) {
		if sm, ok := msg.(SwitchMsg); ok {
			return sm.Next
		}
	}
	return nil
}

func TestScanModel(t *testing.T) {
	reviewNext := func() (tea.Model, error) { return &stubScreen{view: "review"}, nil }

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// A scan is run in order to review it, so finishing switches straight
		// into the prefetched review — no prompt in the way.
		{"SwitchesToReviewWithoutAsking", func(t *testing.T) {
			m := NewScanModel(ScanConfig{ReviewNext: reviewNext})
			prefetched := &stubScreen{view: "review"}

			next, _ := m.Update(reviewReadyMsg{model: prefetched})
			_, cmd := next.(ScanModel).Update(scanDoneMsg{})
			if got := switchTarget(cmd); got != tea.Model(prefetched) {
				t.Fatalf("finishing should switch to the prefetched review, got %v", got)
			}
		}},
		// The prefetch hasn't landed yet: park on "Opening review…" and switch
		// when it does, rather than dropping back to a prompt.
		{"WaitsForTheStillRunningPrefetch", func(t *testing.T) {
			m := NewScanModel(ScanConfig{ReviewNext: reviewNext})
			m.reviewFetching = true

			next, cmd := m.Update(scanDoneMsg{})
			if switchTarget(cmd) != nil {
				t.Fatal("nothing to switch to yet")
			}
			m = next.(ScanModel)
			if v := ansi.Strip(m.View()); !strings.Contains(v, "Opening review…") {
				t.Errorf("want the loading footer while the prefetch runs:\n%s", v)
			}
			landed := &stubScreen{view: "review"}
			_, cmd = m.Update(reviewReadyMsg{model: landed})
			if got := switchTarget(cmd); got != tea.Model(landed) {
				t.Errorf("the arriving review should switch straight in, got %v", got)
			}
		}},
		// Warn-once-then-act: the first ctrl+c cancels and says what that
		// costs, the second gives up on a pipeline that won't unwind. Without
		// the second, a wedged phase leaves the screen unquittable.
		{"SecondCtrlCQuitsAWedgedPipeline", func(t *testing.T) {
			cancelled := 0
			m := NewScanModel(ScanConfig{Cancel: func() { cancelled++ }})

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			m = next.(ScanModel)
			if cmd != nil {
				t.Fatal("the first ctrl+c should cancel and wait, not quit")
			}
			if cancelled != 1 || !m.Cancelled() {
				t.Fatalf("first ctrl+c should cancel the pipeline: calls=%d cancelled=%v", cancelled, m.Cancelled())
			}
			if v := ansi.Strip(m.View()); !strings.Contains(v, "press ctrl+c again to quit") {
				t.Errorf("want the warning above the footer:\n%s", v)
			}

			_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			if cmd == nil || flattenCmd(cmd)[0] != tea.Msg(tea.QuitMsg{}) {
				t.Errorf("the second ctrl+c should quit, got %v", flattenCmd(cmd))
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}
