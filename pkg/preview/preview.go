// Package preview owns the cache of peek copies a review makes of a folder's
// files, so any front end can make, reuse and clean them alike.
package preview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/atomicfile"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

const (
	// maxPreviewBytes caps one peek.
	maxPreviewBytes = 250 * 1024 * 1024
	// previewBudgetDivisor makes the budget for one library's preview copies
	// 5% of the temp volume (free space plus whatever the copies already hold).
	previewBudgetDivisor = 20
	// copyingPrefix names a copy in progress; it is renamed to its hash name
	// only when complete, so the hash name existing means finished
	copyingPrefix = ".copying-"
	// abandonedCopyAge is when a copy in progress counts as left by a killed
	// process: one peek copies at most maxPreviewBytes, in far less time
	abandonedCopyAge = time.Hour
)

// Root is where the library's peek copies live, under the system temp folder
// ($TMPDIR); fixed per library so copies are reused across sessions, and kept
// apart from other libraries' so cleaning one never touches another's.
func Root(library string) string { return filepath.Join(allRoot(), hash(library)) }

// allRoot holds every library's Root.
func allRoot() string { return filepath.Join(os.TempDir(), "wandersort-previews") }

// Clean removes the library's preview copies.
func Clean(library string) error { return os.RemoveAll(Root(library)) }

// CleanAll removes every library's preview copies.
func CleanAll() error { return os.RemoveAll(allRoot()) }

// DirFor keys a preview by a hash of its file list, so a parent and its
// only-child leaf share one copy, in every session.
func DirFor(library string, files []string) string {
	sorted := slices.Clone(files)
	slices.Sort(sorted)
	return filepath.Join(Root(library), hash(strings.Join(sorted, "\x00")))
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// Copy copies up to maxPreviewBytes of files (native paths) into their
// preview dir, reusing a finished copy, and returns the dir for a viewer.
// Older copies are evicted to keep the library's copies in budget.
func Copy(ctx context.Context, library string, files []string) (string, error) {
	root := Root(library)
	dir := DirFor(library, files)
	if _, err := os.Stat(dir); err == nil {
		touch(dir) // makeRoom evicts by mtime, so a reuse is a use
		return dir, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	// Best-effort: a volume that won't report its free space evicts nothing
	// rather than refusing the peek.
	if free, err := volume.FreeBytes(root); err == nil {
		if err := makeRoom(root, plannedBytes(files), int64(free)); err != nil {
			return "", err
		}
	}

	// copy aside, then rename into place (same filesystem, atomic); a killed
	// copy leaves only a .copying- dir, never a cache hit
	tmp, err := os.MkdirTemp(root, copyingPrefix+"*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp) // no-op once the rename succeeded
	if _, err := copyFiles(ctx, files, tmp, maxPreviewBytes); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// another process copying the same folder got there first; its copy
		// is this copy, so take it
		if _, statErr := os.Stat(dir); statErr != nil {
			return "", err
		}
	}
	return dir, nil
}

// plannedBytes is what copyFiles will write for these files; unstattable files
// count as zero.
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

// touch marks a preview copy as just used, so eviction is least-recently-used.
// Best-effort.
func touch(dir string) {
	now := time.Now()
	_ = os.Chtimes(dir, now, now)
}

// makeRoom evicts least-recently-used preview copies until need fits in 5% of
// the volume's free space plus what the copies hold.
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
		// a copy in progress is another peek's: counted, never evicted, unless
		// a killed process left it long ago
		if strings.HasPrefix(e.Name(), copyingPrefix) && time.Since(fi.ModTime()) < abandonedCopyAge {
			continue
		}
		dirs = append(dirs, copyDir{p, fi.ModTime().UnixNano(), size})
	}

	budget := (free + used) / previewBudgetDivisor
	if need > budget {
		return fmt.Errorf("preview needs %d MB; only %d MB allowed",
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

// copyFiles copies srcPaths into destDir, stopping once maxBytes has been
// copied. Sources are never modified. Returns the files copied.
func copyFiles(ctx context.Context, srcPaths []string, destDir string, maxBytes int64) (int, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return 0, fmt.Errorf("create dest dir %s: %w", destDir, err)
	}
	var total int64
	copied := 0
	for _, src := range srcPaths {
		if ctx.Err() != nil {
			return copied, ctx.Err()
		}
		if total >= maxBytes {
			break
		}
		dest := filepath.Join(destDir, filepath.Base(src))
		n, err := atomicfile.Copy(src, dest, nil, nil)
		if errors.Is(err, fs.ErrExist) {
			continue // two sources share a basename; a preview needs only one
		}
		if err != nil {
			return copied, err
		}
		total += n
		copied++
	}
	return copied, nil
}
