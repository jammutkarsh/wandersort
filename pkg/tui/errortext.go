package tui

import (
	"errors"

	"github.com/charmbracelet/x/ansi"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/explain"
)

// RestoreHint is the command that brings back a library found with only its backup.
const RestoreHint = "Run 'wandersort admin db --restore -o <its folder>' to put the backup back."

// ErrorText is explain.Error for a person at the terminal: with the command
// that helps, where there is one. explain names none, as every front end uses it.
func ErrorText(err error) string {
	text := explain.Error(err)
	if errors.Is(err, config.ErrBackupOnly) {
		text += " " + RestoreHint
	}
	return text
}

// errorLines is an error line wrapped to width: what to do about it comes
// last, so it is never cut. 0 is a width not known yet.
func errorLines(line string, width int) string {
	if width <= 0 {
		return line
	}
	return ansi.Wrap(line, width, "")
}
