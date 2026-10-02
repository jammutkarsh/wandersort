package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func newTestReady() ReadyModel {
	m := NewReadyModel(ReadyItem{Phase: "exiftool", Label: "exiftool"}, ReadyItem{Phase: "location", Label: "locationDB"})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return next.(ReadyModel)
}

func released(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestReadyRetryReleasesOnEnterOrCountdown(t *testing.T) {
	t.Run("enter", func(t *testing.T) {
		m := newTestReady()
		retry := make(chan struct{})
		next, _ := m.Update(RetryMsg{Failed: map[string]string{"location": "no connection"}, Next: 2, Tries: 3, Go: retry})
		view := ansi.Strip(next.View())
		if !strings.Contains(view, "better network") || !strings.Contains(view, "try 2 of 3") || !strings.Contains(view, "no connection") {
			t.Errorf("retry screen must ask for a better network and say which try:\n%s", view)
		}
		if released(retry) {
			t.Fatal("retry released before enter or the countdown")
		}
		next, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if !released(retry) {
			t.Error("enter must release the retry")
		}
		// a stale tick after enter must not close the channel twice
		next.Update(retryTickMsg{gen: next.(ReadyModel).gen - 1})
	})
	t.Run("countdown", func(t *testing.T) {
		m := newTestReady()
		retry := make(chan struct{})
		next, _ := m.Update(RetryMsg{Failed: map[string]string{"location": "x"}, Next: 2, Tries: 3, Go: retry})
		for range int(retryCountdown / time.Second) {
			next, _ = next.Update(retryTickMsg{gen: next.(ReadyModel).gen})
		}
		if !released(retry) {
			t.Error("the countdown reaching zero must release the retry")
		}
	})
}

func TestReadyGaveUpQuitsOnAnyKey(t *testing.T) {
	m := newTestReady()
	next, _ := m.Update(DepsFailedMsg{Failed: map[string]string{"location": "no connection"}, Tries: 3})
	if view := ansi.Strip(next.View()); !strings.Contains(view, "Couldn't download locationDB after 3 tries") {
		t.Errorf("give-up screen must name the dependency and the tries:\n%s", view)
	}
	_, cmd := next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if cmd == nil {
		t.Fatal("any key after giving up must leave")
	}
	if l, ok := cmd().(Leave); !ok || !l.Quit {
		t.Errorf("leave = %#v, want a quit", cmd())
	}
}
