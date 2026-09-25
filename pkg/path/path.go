// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

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

// SanitizeSegment makes a derived value safe to use as a single path segment
// — a folder name, not a full path. The one place this decides what a
// derived name is allowed to contain, so vfs (device/orientation/media/date
// segments, renames) and location (the rename dropdown's folder value) apply
// the same rule instead of two packages agreeing on it by convention.
//
// Safe means safe on every filesystem a library can sit on, not just this
// machine's: photo drives are usually exFAT or NTFS, which refuse the
// characters in unportable and the names in reserved, so a folder APFS would
// accept still fails the copy there.
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

// SanitizeFileName makes a file's own name safe to create on any filesystem
// a library can sit on — the same characters and reserved names as
// SanitizeSegment, but a file name otherwise stays as the camera wrote it:
// spaces, dots and case are kept, and only what a filesystem would refuse
// changes. The stem is cut to leave room for the extension and a collision
// suffix (_N), so the name still fits once one is added.
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
	// maxNameBytes is the longest name every supported filesystem takes:
	// 255 bytes on ext4/APFS, 255 UTF-16 units on exFAT/NTFS, which any
	// 255-byte UTF-8 name is within.
	maxNameBytes = 255
	// suffixRoom is what a collision suffix may add to a file name: "_" and
	// up to seven digits.
	suffixRoom = 8
	// maxExtBytes bounds what counts as an extension rather than a stem
	// that happens to hold a dot.
	maxExtBytes = 16
)

// unportable reports whether r is refused in a name by some filesystem a
// library may live on: the separators, what exFAT and NTFS forbid, and
// control characters.
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

// ReduceRoots canonicalizes each path, validates it is a directory,
// deduplicates, and prunes any path strictly nested under another.
// Returns the minimal set of roots to scan.
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

	// Compared against every accepted root, not just the last: a lex sort does
	// not keep a folder's descendants next to it — "/a b" sorts between "/a"
	// and "/a/c", because a space sorts before the separator — so a
	// last-only check kept "/a/c" and walked it twice. Roots are a handful,
	// so the quadratic loop costs nothing.
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
