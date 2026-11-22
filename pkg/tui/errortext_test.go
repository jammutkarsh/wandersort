package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
)

// A library no screen can open is named with the whole command that helps.
func TestScreensNameTheCommandThatHelps(t *testing.T) {
	t.Run("only its backup", func(t *testing.T) {
		checkScreensShow(t, fmt.Errorf("/photos: %w", config.ErrBackupOnly), RestoreHint)
	})
	t.Run("old hashes", func(t *testing.T) {
		checkScreensShow(t, fmt.Errorf("app db: %w", db.ErrOldHashes), NewLibraryHint)
	})
}

// checkScreensShow checks the screens that can meet err show hint, whole.
func checkScreensShow(t *testing.T, err error, hint string) {
	t.Helper()
	size := tea.WindowSizeMsg{Width: 80, Height: 24}

	var home tea.Model = NewHomeModel(HomeConfig{})
	home, _ = home.Update(size)
	home, _ = home.Update(HomeErrMsg{Err: err})

	var form tea.Model = NewFormModel([]*Field{{
		Kind: FieldInput, Title: "Library folder",
		Validator: func(string) error { return err },
	}}, func() error { return nil })
	form, _ = form.Update(size)
	form, _ = form.Update(tea.KeyMsg{Type: tea.KeyEnter})

	for name, screen := range map[string]tea.Model{"home": home, "form": form} {
		view := ansi.Strip(screen.View())
		if !strings.Contains(strings.Join(strings.Fields(view), " "), hint) {
			t.Errorf("%s doesn't show %q:\n%s", name, hint, view)
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
