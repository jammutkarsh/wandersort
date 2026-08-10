// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

const (
	// maxPreviewBytes caps one peek.
	maxPreviewBytes = 250 * 1024 * 1024
	// previewBudgetDivisor makes the budget for all preview copies together 5%
	// of the temp volume (free space plus whatever the copies already hold).
	previewBudgetDivisor = 20
	// copyingPrefix names a copy still in progress. It is renamed onto its final
	// hash name only once every file is in it, so a directory under that name
	// existing at all means the copy finished.
	copyingPrefix = ".copying-"
)

/* --- preview: copy up to maxPreviewBytes of a folder's files to a temp dir and open it --- */

type previewDoneMsg struct {
	dir string
	err error
}

// previewRootDir is a var only so tests can point it at a disposable
// directory — nothing else reassigns it.
var previewRootDir = filepath.Join(os.TempDir(), "wandersort-previews")

// PreviewRoot is where every peek copy lives. Fixed, not an os.MkdirTemp name,
// so a copy made in one session is still there — and still reused — in the
// next. Cleaned by a finished review and by `wandersort reset`.
func PreviewRoot() string { return previewRootDir }

// CleanPreviews removes every preview copy.
func CleanPreviews() error {
	return os.RemoveAll(PreviewRoot())
}

// previewDirFor keys a preview by file membership, not node ID: a parent and
// its only-child leaf carry the same files and share one copy. The name is a
// hash of that membership, so the same folder maps to the same directory in
// every session.
func previewDirFor(files []string) string {
	sorted := slices.Clone(files)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\x00")))
	return filepath.Join(PreviewRoot(), hex.EncodeToString(sum[:8]))
}

// peekCmd copies a folder's files to its preview dir for the OS viewer,
// reusing the copy from this or any earlier session if it is already there.
// Runs off the UI goroutine.
func peekCmd(ctx context.Context, database *db.DB, node *vfs.Node) tea.Cmd {
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

		dir := previewDirFor(files)
		if _, err := os.Stat(dir); err == nil {
			touch(dir) // makeRoom evicts by mtime, so a reuse is a use
			return previewDoneMsg{dir: dir}
		}

		if err := os.MkdirAll(PreviewRoot(), 0o755); err != nil {
			return previewDoneMsg{err: err}
		}
		// Best-effort: a volume that won't report its free space evicts nothing
		// rather than refusing the peek.
		if free, err := volume.FreeBytes(PreviewRoot()); err == nil {
			if err := makeRoom(PreviewRoot(), plannedBytes(files), int64(free)); err != nil {
				return previewDoneMsg{err: err}
			}
		}

		// Copy aside, then rename into place: the rename is atomic (same
		// directory, so same filesystem), which is what lets the hash name mean
		// "complete" on its own. A copy killed partway leaves only a .copying-
		// directory, which is never a cache hit and which makeRoom evicts like
		// any other.
		tmp, err := os.MkdirTemp(PreviewRoot(), copyingPrefix+"*")
		if err != nil {
			return previewDoneMsg{err: err}
		}
		defer os.RemoveAll(tmp) // no-op once the rename succeeded
		if _, err := copyFiles(ctx, files, tmp, maxPreviewBytes, nil); err != nil {
			return previewDoneMsg{err: err}
		}
		if err := os.Rename(tmp, dir); err != nil {
			// another process copying the same folder got there first; its copy
			// is this copy, so take it
			if _, statErr := os.Stat(dir); statErr != nil {
				return previewDoneMsg{err: err}
			}
		}
		return previewDoneMsg{dir: dir}
	}
}

// plannedBytes is what copyFiles will write for these files: their total size,
// stopping at the same point the copy does. Unstattable files count as zero —
// the copy will fail on them anyway.
func plannedBytes(files []string) int64 {
	var total int64
	for _, f := range files {
		if total >= maxPreviewBytes {
			break
		}
		if fi, err := os.Stat(f); err == nil {
			total += fi.Size()
		}
	}
	return total
}

// touch marks a preview copy as just used, so makeRoom's mtime order is
// least-recently-opened rather than oldest-created: peeking a dozen folders in
// one session must not evict the one being looked at now. Best-effort — a
// failed touch only makes that copy a likelier eviction.
func touch(dir string) {
	now := time.Now()
	_ = os.Chtimes(dir, now, now)
}

// makeRoom evicts the least recently opened preview copies until need bytes
// fit in the budget: 5% of what the volume would have free with no copies on
// it (free plus what they already hold). An unreadable root evicts nothing.
func makeRoom(root string, need, free int64) error {
	type copyDir struct {
		path string
		mod  int64
		size int64
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	dirs := make([]copyDir, 0, len(entries))
	var used int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(root, e.Name())
		size := dirSize(p)
		used += size
		dirs = append(dirs, copyDir{p, fi.ModTime().UnixNano(), size})
	}

	budget := (free + used) / previewBudgetDivisor
	if need > budget {
		return fmt.Errorf("preview needs %d MB, more than the %d MB preview budget (5%% of this disk)",
			need/(1024*1024), budget/(1024*1024))
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod < dirs[j].mod })
	for _, d := range dirs {
		if used+need <= budget {
			break
		}
		if os.RemoveAll(d.path) == nil {
			used -= d.size
		}
	}
	return nil
}

// dirSize sums a preview copy's files. Preview dirs are flat (copyFiles writes
// base names into one directory), so there is nothing to recurse into.
func dirSize(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if fi, err := e.Info(); err == nil {
			total += fi.Size()
		}
	}
	return total
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
