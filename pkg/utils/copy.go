package utils

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CopyProgress reports incremental progress of a CopyFiles run — called after
// each file finishes, with that file's path, its size, and the running total
// across the whole batch. A caller driving a progress bar or spinner can use
// it to report as it goes; nil is fine if the caller doesn't care.
type CopyProgress func(srcPath string, fileBytes, totalBytes int64)

// CopyFiles copies srcPaths into destDir, one at a time, stopping once at
// least maxBytes total has been copied (0 = no cap — copies everything).
// Sources are never modified. Returns the number of files actually copied.
//
// This is the shared copy primitive: the review TUI's preview (internal/cli)
// uses it to stage a size-capped sample for the OS file viewer, and it's
// meant to be the same primitive a future Execute phase reuses for the real
// copy-then-verify-then-delete library move — CopyFile alone (no cap) is the
// piece that phase would call directly.
func CopyFiles(ctx context.Context, srcPaths []string, destDir string, maxBytes int64, onProgress CopyProgress) (int, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return 0, fmt.Errorf("create dest dir %s: %w", destDir, err)
	}

	var total int64
	copied := 0
	for _, src := range srcPaths {
		if ctx.Err() != nil {
			return copied, ctx.Err()
		}
		if maxBytes > 0 && total >= maxBytes {
			break
		}

		dest := filepath.Join(destDir, filepath.Base(src))
		n, err := CopyFile(src, dest)
		if err != nil {
			return copied, err
		}
		total += n
		copied++
		if onProgress != nil {
			onProgress(src, n, total)
		}
	}
	return copied, nil
}

// CopyFile copies src to dest atomically — a temp file in dest's directory,
// then a rename — so a failure or cancel partway through never leaves a
// partial file at dest. src is opened read-only and never modified. Returns
// bytes written.
func CopyFile(src, dest string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".copy-*")
	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op if Rename succeeded
	}()

	n, err := io.Copy(tmp, in)
	if err != nil {
		return 0, fmt.Errorf("copy %s: %w", src, err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return 0, fmt.Errorf("rename to %s: %w", dest, err)
	}
	return n, nil
}
