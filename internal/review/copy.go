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
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/atomicfile"
)

// copyProgress reports a copyFiles run after each file: that file's path and
// size, plus the batch's running total. nil if the caller doesn't care.
type copyProgress func(srcPath string, fileBytes, totalBytes int64)

// copyFiles copies srcPaths into destDir, stopping once maxBytes has been
// copied (0 = no cap). Sources are never modified. Returns the files copied.
func copyFiles(ctx context.Context, srcPaths []string, destDir string, maxBytes int64, onProgress copyProgress) (int, error) {
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
		n, err := atomicfile.Copy(src, dest)
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
