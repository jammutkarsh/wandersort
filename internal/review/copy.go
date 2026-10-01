package review

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/atomicfile"
)

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
