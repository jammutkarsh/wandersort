package tui

import (
	"errors"

	"github.com/charmbracelet/x/ansi"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/explain"
)

// RestoreHint is the command that puts a library's backup back.
const RestoreHint = "Run 'wandersort admin db --restore -o <its folder>' to put the backup back."

// NeedsRestore reports whether err is a library only its backup helps.
func NeedsRestore(err error) bool { return errors.Is(err, config.ErrBackupOnly) || db.Damaged(err) }

// ErrorText is explain.Error for a person at the terminal: with the command
// that helps, where there is one. explain names none, as every front end uses it.
func ErrorText(err error) string {
	text := explain.Error(err)
	if NeedsRestore(err) {
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
