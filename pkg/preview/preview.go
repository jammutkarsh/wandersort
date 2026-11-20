// Package preview owns the cache of peek copies a review makes of a folder's
// files, so any front end can make, reuse and clean them alike.
package preview

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Root is where every peek copy lives, under the system temp folder ($TMPDIR);
// fixed so copies are reused across sessions. Cleaned once a copy applies the
// review's edits, and by `admin clear`.
func Root() string { return filepath.Join(os.TempDir(), "wandersort-previews") }

// Clean removes every preview copy.
func Clean() error { return os.RemoveAll(Root()) }

// DirFor keys a preview by a hash of its file list, so a parent and its
// only-child leaf share one copy, in every session.
func DirFor(files []string) string {
	sorted := slices.Clone(files)
	slices.Sort(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\x00")))
	return filepath.Join(Root(), hex.EncodeToString(sum[:8]))
}
