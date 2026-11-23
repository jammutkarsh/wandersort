package tui

import (
	"errors"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/explain"
)

// itsFolder stands for the library's folder in a hint that doesn't know it.
const itsFolder = "<its folder>"

// RestoreHint is the command that puts a library's backup back.
const RestoreHint = "Run 'wandersort admin db --restore -o " + itsFolder + "' to put the backup back."

// NewLibraryHint is the command that makes a new library of the photos in one
// this version can't open.
const NewLibraryHint = "Run 'wandersort add -o <new folder> -p " + itsFolder + ",<folders not copied yet>' to start a new library from its photos."

// NeedsRestore reports whether err is a library only its backup helps.
func NeedsRestore(err error) bool { return errors.Is(err, config.ErrBackupOnly) || db.Damaged(err) }

// Hint is the command that helps with err, or "" where there is none.
func Hint(err error) string {
	switch {
	case NeedsRestore(err):
		return RestoreHint
	case errors.Is(err, db.ErrOldHashes):
		return NewLibraryHint
	}
	return ""
}

// HintIn is Hint with the library's folder dir written in, quoted when a shell
// would split it.
func HintIn(err error, dir string) string {
	if strings.ContainsAny(dir, " '\"") {
		dir = `"` + dir + `"`
	}
	return strings.Replace(Hint(err), itsFolder, dir, 1)
}

// ErrorText is explain.Error for a person at the terminal: with the command
// that helps, where there is one. explain names none, as every front end uses it.
func ErrorText(err error) string {
	text := explain.Error(err)
	if hint := Hint(err); hint != "" {
		text += "\n" + hint // its own line, so no flag is split at its hyphen
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
