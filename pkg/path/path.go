package path

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type Resolver struct {
	HomeDir string
}

func New() *Resolver {
	home, _ := os.UserHomeDir()
	return &Resolver{HomeDir: home}
}

func (r *Resolver) IsDirectory(path string) (bool, error) {
	if p, err := r.RealPath(path); err != nil {
		return false, err
	} else {
		path = p
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("stat %q: %w", path, err)
	}
	return fileInfo.IsDir(), nil
}

// RealPath resolves symlinks and returns the canonical absolute path of p
func (r *Resolver) RealPath(p string) (string, error) {
	p = r.ExpandPath(p)
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("eval symlinks %q: %w", p, err)
	}
	absPath, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("abs %q: %w", resolved, err)
	}
	return absPath, nil
}

// ExpandPath expands a leading "~" or "~/" to the user's home directory.
// Non-home-relative paths are returned unchanged
func (r *Resolver) ExpandPath(path string) string {
	if path == "~" {
		return r.HomeDir
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		return filepath.Join(r.HomeDir, path[2:])
	}
	return path
}

// RelativeToHome converts an absolute path to
// a path relative wrt user's home directory if it is under the home directory
func (r *Resolver) RelativeToHome(path string) string {
	cleanPath := filepath.Clean(path)
	home := filepath.Clean(r.HomeDir)

	if cleanPath == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(cleanPath, prefix) {
		suffix := strings.TrimPrefix(cleanPath, home)
		return "~" + suffix
	}

	return path
}

// SanitizeSegment makes a derived value safe as one path segment on every
// filesystem a library may sit on (exFAT and NTFS refuse more than APFS).
func SanitizeSegment(seg string) string {
	// commas are fine in a name a person is *choosing* (geocode results, a
	// rename dropdown) — just not once picked, so strip them here.
	seg = strings.Map(func(r rune) rune {
		switch {
		case r == ' ', r == ',', unportable(r):
			return '-'
		}
		return r
	}, seg)
	for strings.Contains(seg, "--") {
		seg = strings.ReplaceAll(seg, "--", "-")
	}
	seg = strings.Trim(truncate(strings.Trim(seg, " ._-"), maxNameBytes), " ._-")
	if seg == "" {
		return "-"
	}
	return unreserve(seg)
}

// SanitizeFileName makes a file name safe on every library filesystem, keeping
// spaces, dots and case, and leaves room for the extension and a _N suffix.
func SanitizeFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unportable(r) {
			return '-'
		}
		return r
	}, name)
	ext := filepath.Ext(name)
	if len(ext) > maxExtBytes {
		ext = ""
	}
	stem := strings.TrimRight(strings.TrimSuffix(name, ext), " .")
	stem = strings.TrimRight(truncate(stem, maxNameBytes-len(ext)-suffixRoom), " .")
	if stem == "" {
		stem = "-"
	}
	return unreserve(stem) + ext
}

const (
	// maxNameBytes: 255 bytes (ext4/APFS) or 255 UTF-16 units (exFAT/NTFS),
	// which any 255-byte UTF-8 name fits
	maxNameBytes = 255
	// suffixRoom is what a collision suffix may add to a file name: "_" and
	// up to seven digits.
	suffixRoom = 8
	// maxExtBytes bounds what counts as an extension rather than a stem
	// that happens to hold a dot.
	maxExtBytes = 16
)

// unportable reports whether some library filesystem refuses r in a name.
func unportable(r rune) bool {
	switch r {
	case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
		return true
	}
	return r < 0x20 || r == 0x7f
}

// truncate cuts s to at most n bytes without splitting a character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// unreserve suffixes a name Windows reserves for a device (CON, NUL, COM1…),
// which it refuses as a file or folder name with or without an extension.
func unreserve(name string) string {
	stem := strings.ToUpper(name)
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	switch stem {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		i := len(stem)
		return name[:i] + "_" + name[i:]
	}
	return name
}

// Overlaps reports whether a and b name the same directory or one is nested
// inside the other. Both must already be canonical absolute paths
func Overlaps(a, b string) bool {
	sep := string(filepath.Separator)
	// the filesystem root contains every absolute path
	if a == b || a == sep || b == sep {
		return true
	}
	return strings.HasPrefix(b, a+sep) || strings.HasPrefix(a, b+sep)
}

// ReduceRoots canonicalizes paths, checks they are directories, deduplicates,
// and drops any nested under another.
func ReduceRoots(r *Resolver, paths []string) ([]string, error) {
	canonicalSet := make(map[string]struct{}, len(paths))

	for _, p := range paths {
		cleaned := filepath.Clean(p)
		resolved, err := r.RealPath(cleaned)
		if err != nil {
			return nil, err
		}

		isDir, err := r.IsDirectory(resolved)
		if err != nil {
			return nil, err
		}
		if !isDir {
			return nil, fmt.Errorf("path is not a directory: %s", resolved)
		}

		canonicalSet[resolved] = struct{}{}
	}

	canonicalPaths := make([]string, 0, len(canonicalSet))
	for p := range canonicalSet {
		canonicalPaths = append(canonicalPaths, p)
	}
	sort.Strings(canonicalPaths)

	// check every accepted root, not just the last: a lex sort puts "/a b"
	// between "/a" and "/a/c"
	effectivePaths := make([]string, 0, len(canonicalPaths))
	for _, candidate := range canonicalPaths {
		nested := false
		for _, root := range effectivePaths {
			if isChildPath(root, candidate) {
				nested = true
				break
			}
		}
		if !nested {
			effectivePaths = append(effectivePaths, candidate)
		}
	}

	return effectivePaths, nil
}

// isChildPath reports whether candidate is strictly nested below parent.
// Both paths must already be canonical.
func isChildPath(parent, candidate string) bool {
	if parent == candidate {
		return false
	}
	// Append the separator so "/foo" doesn't falsely match "/foobar"
	return strings.HasPrefix(candidate, parent+string(filepath.Separator))
}
