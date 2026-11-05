package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func TestClickedKeys(t *testing.T) {
	footer := tui.KeyHint("enter", "scan") + "   " + tui.KeyHint("esc", "stop")
	view := "top\n\nbody\n" + footer
	left := func(x, y int) tea.MouseMsg {
		return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}
	}
	escAt := strings.Index(ansi.Strip(footer), "esc")

	tests := []struct {
		name   string
		e      tea.MouseMsg
		height int
		want   string
	}{
		{"hint", left(1, 3), 0, "enter"},
		{"second hint", left(escAt, 3), 0, "esc"},
		{"plain text", left(1, 2), 0, ""},
		{"press, not release", tea.MouseMsg{X: 1, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, 0, ""},
		// a view taller than the terminal shows its bottom rows
		{"scrolled view", left(1, 2), 3, "enter"},
	}
	for _, tt := range tests {
		got := strings.Join(clickedKeys(view, tt.e, tt.height), " ")
		if got != tt.want {
			t.Errorf("%s: keys = %q, want %q", tt.name, got, tt.want)
		}
	}
}
