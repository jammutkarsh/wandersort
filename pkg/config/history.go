// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	historyFileName = "libraries"
	// maxHistory caps the remembered output folders (spec D3). A list nobody
	// scrolls past the first few entries of doesn't need to grow forever.
	maxHistory = 100
)

// History is the output folders this machine has opened, newest first.
// Folders that no longer exist are dropped as it is read — a library on an
// unplugged drive is not one to offer — so the list is only ever as long as
// what is really there.
func (cfg *Configuration) History() []string {
	data, err := os.ReadFile(filepath.Join(cfg.appDir, historyFileName))
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for line := range strings.SplitSeq(string(data), "\n") {
		dir := strings.TrimSpace(line)
		if dir == "" || seen[dir] {
			continue
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
		if len(out) == maxHistory {
			break
		}
	}
	return out
}

// Remember puts dir at the front of the history, dropping the oldest entries
// past maxHistory. Called when a library is opened, which is the only moment
// a folder is known to really be one.
func (cfg *Configuration) Remember(dir string) error {
	list := append([]string{dir}, cfg.History()...)
	for i := 1; i < len(list); i++ {
		if list[i] == dir {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(list) > maxHistory {
		list = list[:maxHistory]
	}
	if err := os.MkdirAll(cfg.appDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", cfg.appDir, err)
	}
	path := filepath.Join(cfg.appDir, historyFileName)
	if err := os.WriteFile(path, []byte(strings.Join(list, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
