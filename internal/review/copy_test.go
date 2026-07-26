// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The size cap is what keeps a peek at a 600GB folder from filling the disk,
// so it gets the one check: copying stops once maxBytes is reached.
func TestCopyFilesStopsAtMaxBytes(t *testing.T) {
	src := t.TempDir()
	dest := filepath.Join(t.TempDir(), "preview")

	var paths []string
	for _, name := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		p := filepath.Join(src, name)
		if err := os.WriteFile(p, make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	// 150 bytes admits the first file, then the second (the cap is checked
	// before each copy, not after), and stops before the third
	copied, err := copyFiles(context.Background(), paths, dest, 150, nil)
	if err != nil {
		t.Fatalf("copyFiles: %v", err)
	}
	if copied != 2 {
		t.Errorf("copied = %d, want 2", copied)
	}

	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("dest holds %d files, want 2", len(entries))
	}
}
