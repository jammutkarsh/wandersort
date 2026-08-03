// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// stubScreen is a minimal tea.Model that records what it was sent, standing
// in for a real screen (scan/review) in Shell tests.
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

func TestShellForwardsMessagesToCurrentScreen(t *testing.T) {
	first := &stubScreen{view: "first"}
	shell := NewShell(first)

	if shell.Current() != first {
		t.Fatalf("Current() should return the screen passed to NewShell")
	}
	if shell.View() != "first" {
		t.Errorf("View() = %q, want %q", shell.View(), "first")
	}

	model, _ := shell.Update(tea.KeyMsg{Type: tea.KeyEnter})
	shell = model.(Shell)
	if len(first.msgs) != 1 {
		t.Fatalf("expected the key message forwarded to the current screen, got %d msgs", len(first.msgs))
	}
}

func TestShellTracksWindowSizeAndReseedsOnSwitch(t *testing.T) {
	first := &stubScreen{view: "first"}
	shell := NewShell(first)

	model, _ := shell.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	shell = model.(Shell)

	next := &stubScreen{view: "second"}
	model, cmd := shell.Update(SwitchMsg{Next: next})
	shell = model.(Shell)
	if shell.Current() != next {
		t.Fatalf("SwitchMsg should replace the current screen")
	}
	if cmd == nil {
		t.Fatalf("switching must return a cmd that re-seeds Init and the current size")
	}

	msgs := flattenCmd(cmd)
	var sawResize bool
	for _, msg := range msgs {
		if wsm, ok := msg.(tea.WindowSizeMsg); ok {
			sawResize = true
			if wsm.Width != 80 || wsm.Height != 24 {
				t.Errorf("re-seeded size = %+v, want {80 24}", wsm)
			}
		}
	}
	if !sawResize {
		t.Errorf("switching to a new screen must hand it the shell's current size, got msgs %+v", msgs)
	}
}

func TestShellSwitchToNilQuits(t *testing.T) {
	shell := NewShell(&stubScreen{view: "first"})

	_, cmd := shell.Update(SwitchMsg{Next: nil})
	if cmd == nil {
		t.Fatalf("SwitchMsg{Next: nil} must return a cmd")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("SwitchMsg{Next: nil} should quit, got %T", msg)
	}
}

func TestShellInitDelegatesToCurrentScreen(t *testing.T) {
	wantCmd := func() tea.Msg { return "init-sentinel" }
	first := &stubScreen{view: "first", initCmd: wantCmd}
	shell := NewShell(first)

	cmd := shell.Init()
	if cmd == nil {
		t.Fatalf("Init() returned nil, want the screen's init cmd")
	}
	if msg := cmd(); msg != "init-sentinel" {
		t.Errorf("Init() cmd = %v, want the screen's own init cmd result", msg)
	}
}
