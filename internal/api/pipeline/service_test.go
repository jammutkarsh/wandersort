// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

func TestComputeScanPaths_FilterDuplicatePaths(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	grandChild := filepath.Join(child, "grand")
	if err := os.MkdirAll(grandChild, 0o755); err != nil {
		t.Fatal(err)
	}

	otherRoot := t.TempDir()

	svc := &Service{logger: logger.NewNoopLogger(), resolver: path.New()}

	resolvedRoot, err := svc.resolver.RealPath(root)
	if err != nil {
		t.Fatal(err)
	}
	resolvedOtherRoot, err := svc.resolver.RealPath(otherRoot)
	if err != nil {
		t.Fatal(err)
	}

	paths, err := svc.prepareScanRoots([]string{
		grandChild,
		root,
		child,
		root + string(filepath.Separator), // Duplicate with trailing separator
		otherRoot,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(paths) != 2 {
		t.Fatalf("%v", paths)
	}

	have := map[string]bool{}
	for _, p := range paths {
		have[p] = true
	}

	if !have[resolvedRoot] || !have[resolvedOtherRoot] {
		t.Fatalf("%v", paths)
	}
}

func TestPrepareScanRoots_ErrorCases(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) []string
	}{
		{
			name: "nonexistent path",
			setup: func(t *testing.T) []string {
				return []string{"/definitely/not/a/real/path"}
			},
		},
		{
			name: "file path instead of directory",
			setup: func(t *testing.T) []string {
				root := t.TempDir()
				file := filepath.Join(root, "note.txt")
				if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return []string{file}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &Service{logger: logger.NewNoopLogger(), resolver: path.New()}
			_, err := svc.prepareScanRoots(tt.setup(t))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
