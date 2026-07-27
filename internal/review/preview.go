// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
)

const maxPreviewBytes = 250 * 1024 * 1024

/* --- preview: copy up to maxPreviewBytes of a folder's files to a temp dir and open it --- */

type previewDoneMsg struct {
	signature string
	dir       string
	err       error
}

// filesSignature keys a preview by file membership, not node ID: a parent and
// its only-child leaf carry the same files and should share one temp copy.
func filesSignature(files []string) string {
	return strings.Join(files, "\x00")
}

// peekCmd copies a folder's files to a temp dir for the OS viewer, reusing a
// cached copy with the same file membership. cached is a snapshot taken before
// dispatch — this runs off the UI goroutine.
func peekCmd(ctx context.Context, database *db.DB, node *vfs.Node, cached map[string]string) tea.Cmd {
	return func() tea.Msg {
		var files []string
		// a merged node's files still live under the folded-away paths until
		// Confirm rewrites them, so look under each
		for _, id := range append([]string{node.ID}, node.MergedIDs...) {
			under, err := vfs.FilesUnder(ctx, id, database)
			if err != nil {
				return previewDoneMsg{err: err}
			}
			files = append(files, under...)
		}
		if len(files) == 0 {
			return previewDoneMsg{err: fmt.Errorf("no files under %s", node.Name)}
		}
		sig := filesSignature(files)
		if dir, ok := cached[sig]; ok {
			return previewDoneMsg{signature: sig, dir: dir}
		}
		dir, err := os.MkdirTemp("", "wandersort-preview-*")
		if err != nil {
			return previewDoneMsg{err: err}
		}
		if _, err := copyFiles(ctx, files, dir, maxPreviewBytes, nil); err != nil {
			// nothing records this dir on the error path, so cleanupPreviewDirs
			// would never see it — drop the partial copy here instead
			os.RemoveAll(dir)
			return previewDoneMsg{err: err}
		}
		return previewDoneMsg{signature: sig, dir: dir}
	}
}

// cleanupPreviewDirs removes every temp dir peek copied files into — called
// once the review TUI exits, however it exits (confirm, quit, ctrl-c), so a
// review session never leaves preview copies behind.
func cleanupPreviewDirs(dirs map[string]string) {
	for _, dir := range dirs {
		os.RemoveAll(dir)
	}
}

// openInViewer opens a file or folder in the OS default viewer, best-effort.
func openInViewer(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	_ = cmd.Start()
}
