// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package path

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathUtil_ExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home dir: %v", err)
	}

	pu := &Resolver{HomeDir: home}

	tests := []struct {
		input string
		want  string
	}{
		{"~/Photos", filepath.Join(home, "Photos")},
		{"~/Photos/2023/trip", filepath.Join(home, "Photos/2023/trip")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~", home},                    // bare ~ is the home dir itself
		{"~notauser/x", "~notauser/x"}, // ~ only expands as a whole segment
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := pu.ExpandPath(tt.input)
			if got != tt.want {
				t.Errorf("ExpandPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPathUtil_ContractPath(t *testing.T) {
	pu := &Resolver{HomeDir: "/home/testuser"}

	tests := []struct {
		input string
		want  string
	}{
		{"/home/testuser/Photos/2023", "~/Photos/2023"},
		{"/home/testuser", "~"},
		{"/other/path", "/other/path"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := pu.RelativeToHome(tt.input)
			if got != tt.want {
				t.Errorf("ContractPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestOverlaps(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"equal", "/photos", "/photos", true},
		{"child", "/photos", "/photos/sub", true},
		{"parent", "/photos/sub", "/photos", true},
		{"sibling", "/photos", "/videos", false},
		{"prefix but not nested", "/photos", "/photos2", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Overlaps(tt.a, tt.b); got != tt.want {
				t.Errorf("Overlaps(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestReduceRoots_FilterDuplicatePaths(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	grandChild := filepath.Join(child, "grand")
	if err := os.MkdirAll(grandChild, 0o755); err != nil {
		t.Fatal(err)
	}

	otherRoot := t.TempDir()

	r := New()

	resolvedRoot, err := r.RealPath(root)
	if err != nil {
		t.Fatal(err)
	}
	resolvedOtherRoot, err := r.RealPath(otherRoot)
	if err != nil {
		t.Fatal(err)
	}

	paths, err := ReduceRoots(r, []string{
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
		t.Fatalf("expected 2 roots, got %d: %v", len(paths), paths)
	}

	have := map[string]bool{}
	for _, p := range paths {
		have[p] = true
	}

	if !have[resolvedRoot] || !have[resolvedOtherRoot] {
		t.Fatalf("missing expected roots, got: %v", paths)
	}
}

func TestReduceRoots_ErrorCases(t *testing.T) {
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
			r := New()
			_, err := ReduceRoots(r, tt.setup(t))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
