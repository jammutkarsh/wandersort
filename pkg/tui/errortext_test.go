package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jammutkarsh/wandersort/pkg/config"
)

// A library found with only its backup can't be opened from any screen, so
// the screen that says so names the whole command that brings it back.
func TestScreensNameTheRestoreCommand(t *testing.T) {
	backupOnly := fmt.Errorf("/photos: %w", config.ErrBackupOnly)
	size := tea.WindowSizeMsg{Width: 80, Height: 24}

	var home tea.Model = NewHomeModel(HomeConfig{})
	home, _ = home.Update(size)
	home, _ = home.Update(HomeErrMsg{Err: backupOnly})

	var form tea.Model = NewFormModel([]*Field{{
		Kind: FieldInput, Title: "Library folder",
		Validator: func(string) error { return backupOnly },
	}}, func() error { return nil })
	form, _ = form.Update(size)
	form, _ = form.Update(tea.KeyMsg{Type: tea.KeyEnter})

	for name, screen := range map[string]tea.Model{"home": home, "form": form} {
		view := ansi.Strip(screen.View())
		if !strings.Contains(strings.Join(strings.Fields(view), " "), RestoreHint) {
			t.Errorf("%s doesn't show %q:\n%s", name, RestoreHint, view)
		}
		if got := len(strings.Split(view, "\n")); got > size.Height {
			t.Errorf("%s is %d lines in a %d-line window", name, got, size.Height)
		}
		for line := range strings.SplitSeq(view, "\n") {
			if ansi.StringWidth(line) > size.Width {
				t.Errorf("%s: line wider than the window: %q", name, line)
			}
		}
	}
}
