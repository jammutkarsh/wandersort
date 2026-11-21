package review

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/preview"
)

type previewDoneMsg struct {
	err error
	dir string
}

// peekCmd copies a folder's files to its preview dir (reusing an existing copy)
// for the OS viewer, off the UI goroutine.
func peekCmd(ctx context.Context, database *db.DB, library string, node *vfs.Node) tea.Cmd {
	return func() tea.Msg {
		files, err := vfs.NodeFiles(ctx, database, *node)
		if err != nil {
			return previewDoneMsg{err: err}
		}
		if len(files) == 0 {
			return previewDoneMsg{err: fmt.Errorf("no files under %s", node.Name)}
		}
		dir, err := preview.Copy(ctx, library, files)
		return previewDoneMsg{dir: dir, err: err}
	}
}
