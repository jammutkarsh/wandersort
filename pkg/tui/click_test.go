package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestClickTargetsLocateHints(t *testing.T) {
	line := "ab " + KeyHint("enter", "go") + "   " + KeyHint("1-3", "choose") + "   " + KeyHint("esc", "back")
	view := "top\n" + line
	targets := ClickTargets(view)
	if len(targets) != 2 {
		t.Fatalf("targets = %d, want 2 (1-3 is a range, not a key)", len(targets))
	}
	plain := ansi.Strip(line)
	enterAt := strings.Index(plain, "enter")
	escAt := strings.Index(plain, "esc")
	if got, ok := ClickAt(targets, 1, enterAt); !ok || got.Keys[0] != "enter" {
		t.Errorf("click on enter = %+v %v", got, ok)
	}
	if got, ok := ClickAt(targets, 1, escAt+2); !ok || got.Keys[0] != "esc" {
		t.Errorf("click on esc = %+v %v", got, ok)
	}
	if _, ok := ClickAt(targets, 1, 0); ok {
		t.Error("click on plain text hit a target")
	}
	if _, ok := ClickAt(targets, 0, enterAt); ok {
		t.Error("click on the wrong row hit a target")
	}
}

func TestClickTargetsStopAtTruncation(t *testing.T) {
	got := ClickTargets(ansi.Truncate(KeyHint("enter", "go"), 3, ""))
	if len(got) != 1 || got[0].To > 3 {
		t.Errorf("a truncated hint must be clickable only where it shows: %+v", got)
	}
}

func TestClickableKeepsLayout(t *testing.T) {
	plain := "enter go"
	if w := ansi.StringWidth(Clickable(plain, "enter")); w != len(plain) {
		t.Errorf("width = %d, want %d: the markers must take no cells", w, len(plain))
	}
	if got := ansi.Strip(Clickable(plain, "enter")); got != plain {
		t.Errorf("Strip = %q, want %q", got, plain)
	}
}

func TestKeyMsgFor(t *testing.T) {
	cases := map[string]string{
		"enter": "enter", "esc": "esc", "tab": "tab", "shift+tab": "shift+tab",
		"space": " ", "ctrl+c": "ctrl+c", "ctrl+o": "ctrl+o", "?": "?", "r": "r", "1": "1",
	}
	for name, want := range cases {
		if got := KeyMsgFor(name).String(); got != want {
			t.Errorf("KeyMsgFor(%q) = %q, want %q", name, got, want)
		}
	}
	if KeyMsgFor("space").Type != tea.KeySpace {
		t.Error("space is not a KeySpace press")
	}
}
