// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package path

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

func TestSanitizeSegment(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Goa Trip 2024", "Goa-Trip-2024"},
		{"Springfield, Illinois", "Springfield-Illinois"},
		{"a/b\\c:d", "a-b-c-d"},
		{"a---b", "a-b"},
		{"  .leading-and-trailing._  ", "leading-and-trailing"},
		{"", "-"},
		{",,,", "-"},
		{`Why? "Best" <day> | *ever*`, "Why-Best-day-ever"},
		{"tab\there\x01", "tab-here"},
		{"CON", "CON_"},
		{"nul", "nul_"},
		{"Console", "Console"},
		{strings.Repeat("a", 300), strings.Repeat("a", 255)},
		{strings.Repeat("é", 200), strings.Repeat("é", 127)},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := SanitizeSegment(tt.in); got != tt.want {
				t.Errorf("SanitizeSegment(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeFileName(t *testing.T) {
	long := strings.Repeat("a", 300) + ".JPG"
	tests := []struct {
		name, in, want string
	}{
		{"camera name unchanged", "IMG_0001.HEIC", "IMG_0001.HEIC"},
		{"spaces and dots kept", "Goa trip v1.2.jpg", "Goa trip v1.2.jpg"},
		{"refused characters", `a:b?c*"d"<e>|f.jpg`, "a-b-c--d--e--f.jpg"},
		{"control characters", "a\x01b.jpg", "a-b.jpg"},
		{"trailing dots and spaces in the stem", "photo. .jpg", "photo.jpg"},
		{"reserved stem", "con.jpg", "con_.jpg"},
		{"reserved with a longer stem is fine", "cone.jpg", "cone.jpg"},
		{"long stem leaves room for _N", long, strings.Repeat("a", 255-4-8) + ".JPG"},
		{"empty stem", ".jpg", "-.jpg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeFileName(tt.in); got != tt.want {
				t.Errorf("SanitizeFileName(%q) = %q, want %q", tt.in, got, tt.want)
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

// A sibling whose name extends the root's with a character that sorts before
// the separator ("a b" < "a/") lands between a root and its child after the
// sort; the child must still be pruned.
func TestReduceRoots_PrunesChildPastSiblingThatSortsBetween(t *testing.T) {
	base := t.TempDir()
	for _, d := range []string{"a/c", "a b", "a-x"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := New()
	resolved, err := r.RealPath(base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReduceRoots(r, []string{
		filepath.Join(base, "a"), filepath.Join(base, "a b"),
		filepath.Join(base, "a-x"), filepath.Join(base, "a", "c"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(resolved, "a"), filepath.Join(resolved, "a b"), filepath.Join(resolved, "a-x"),
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("ReduceRoots = %v, want %v", got, want)
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
