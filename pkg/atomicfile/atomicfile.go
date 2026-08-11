// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package atomicfile copies one local file to another without ever leaving a
// partial file at the destination. Third caller (review's preview copy,
// execute's Copy adapter, and — soon — the review TUI's copy tests) is where
// this stops being a per-package helper.
package atomicfile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Copy copies src to dest atomically: a temp file in dest's directory, then a
// rename, so a failure partway never leaves a partial file at dest. Creates
// dest's parent directory if needed. Returns bytes written.
func Copy(src, dest string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, fmt.Errorf("create dest dir %s: %w", filepath.Dir(dest), err)
	}

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
