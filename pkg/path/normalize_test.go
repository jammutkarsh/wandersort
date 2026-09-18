// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package path

import (
	"path/filepath"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestToLibrary(t *testing.T) {
	nfc := "Café/й.jpg"
	nfd := norm.NFD.String(nfc)

	tests := []struct {
		name, in, want string
	}{
		{"NFD input", nfd, nfc},
		{"already normalized", nfc, nfc},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToLibrary(tt.in); got != tt.want {
				t.Errorf("ToLibrary(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFromLibrary(t *testing.T) {
	if got, want := FromLibrary("2024/Goa/IMG_1.jpg"), filepath.FromSlash("2024/Goa/IMG_1.jpg"); got != want {
		t.Errorf("FromLibrary = %q, want %q", got, want)
	}
}

// TestToLibrary_WindowsSeparators only exercises anything on windows, where
// filepath.ToSlash actually converts \ to / — elsewhere it's a no-op, and a
// segment this app built never contains \ to begin with.
func TestToLibrary_WindowsSeparators(t *testing.T) {
	if filepath.Separator != '\\' {
		t.Skip("filepath.ToSlash only converts \\ on windows")
	}
	if got, want := ToLibrary(`2024\Goa\IMG_1.jpg`), "2024/Goa/IMG_1.jpg"; got != want {
		t.Errorf("ToLibrary(%q) = %q, want %q", `2024\Goa\IMG_1.jpg`, got, want)
	}
}

// TestToSourcePath_PreservesSpelling guards the bug a real reviewer found:
// forcing a disk-given name to NFC breaks byte-exact lookup on filesystems
// (Linux, Windows) that don't normalize on their own, and can collide two
// distinct Linux files whose names differ only in normalization form.
// ToSourcePath must never fold a name's bytes, only its separator.
func TestToSourcePath_PreservesSpelling(t *testing.T) {
	nfd := norm.NFD.String("Café.jpg") // what a Mac-written file is really named
	if got := ToSourcePath(nfd); got != nfd {
		t.Errorf("ToSourcePath(%q) = %q, want unchanged (no NFC folding)", nfd, got)
	}
}

func TestToSourcePath_PreservesLiteralBackslash(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("backslash is the real separator on this OS")
	}
	// a literal backslash is a legal byte in a Linux filename; unlike a
	// Windows-native path it must not be rewritten to "/"
	in := `a\b.jpg`
	if got := ToSourcePath(in); got != in {
		t.Errorf("ToSourcePath(%q) = %q, want unchanged", in, got)
	}
}

func TestFromSourcePath(t *testing.T) {
	if got, want := FromSourcePath("2024/Goa/IMG_1.jpg"), filepath.FromSlash("2024/Goa/IMG_1.jpg"); got != want {
		t.Errorf("FromSourcePath = %q, want %q", got, want)
	}
}
