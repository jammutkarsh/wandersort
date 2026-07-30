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

	// O(N) prune: after lex sort, any descendant of an accepted root immediately
	// follows it, so comparing against only the last accepted root suffices.
	effectivePaths := make([]string, 0, len(canonicalPaths))
	for _, candidate := range canonicalPaths {
		if len(effectivePaths) == 0 || !isChildPath(effectivePaths[len(effectivePaths)-1], candidate) {
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
