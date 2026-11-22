package review

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
)

type previewDoneMsg struct {
	err error
	dir string
}

// peeker copies a folder's files to its preview copy and returns its folder
// (library.Review.Peek).
type peeker func(context.Context, vfs.Node) (string, error)

// peekCmd peeks into node for the OS viewer, off the UI goroutine.
func peekCmd(ctx context.Context, peek peeker, node *vfs.Node) tea.Cmd {
	return func() tea.Msg {
		dir, err := peek(ctx, *node)
		return previewDoneMsg{dir: dir, err: err}
	}
}
