// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/path"
)

// maxDirSuggestions caps a completion list; a home directory with hundreds of
// folders would otherwise scroll a dropdown nobody reads.
const maxDirSuggestions = 25

// suggestDirs completes like a shell: the directories matching the typed
// prefix, written home-relative. Shared by the config wizard's output-path
// field and the shell's scan-folder input — both complete a directory, and a
// second copy of this would drift from the first.
func suggestDirs(paths *path.Resolver, typed string) []string {
	typed = paths.ExpandPath(strings.TrimSpace(typed))
	if typed == "" {
		return nil
	}
	dir, base := filepath.Split(typed)
	// Typed an existing dir → offer its children, so tab descends a level
	// at a time like shell completion.
	if st, err := os.Stat(typed); err == nil && st.IsDir() {
		dir, base = typed, ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		// bundles (.app, .framework, ...) report IsDir() true but aren't
		// folders a person would pick.
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || isBundleDir(e.Name()) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(e.Name()), strings.ToLower(base)) {
			out = append(out, paths.RelativeToHome(filepath.Join(dir, e.Name())))
			if len(out) == maxDirSuggestions {
				break
			}
		}
	}
	return out
}

// bundleExts are macOS package dirs that report IsDir() true but aren't a folder a person would pick.
// ponytail: extension list, not a bundle-detection API — add here if a report names another one.
var bundleExts = map[string]bool{".app": true}

func isBundleDir(name string) bool {
	return bundleExts[strings.ToLower(filepath.Ext(name))]
}

// toMap converts a slice to a map for multiselect field initialization.
func toMap(items []string) map[string]bool {
	m := make(map[string]bool)
	for _, item := range items {
		m[item] = true
	}
	return m
}
