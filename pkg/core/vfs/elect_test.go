// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import "testing"

func TestIsMeaningfulName(t *testing.T) {
	tests := []struct {
		name string
		stem string
		want bool
	}{
		// Human-named
		{"plain word", "sunset", true},
		{"with hyphen", "wedding-ceremony", true},
		{"with underscore", "Goa_trip", true},
		{"mixed case", "Bali2024", true},
		{"date then word", "20230520_wedding", true},

		// Camera patterns (from DCF spec + known manufacturers)
		{"iPhone", "IMG_3162", false},
		{"Canon image", "IMG_0001", false},
		{"Sony/Nikon", "DSC_1234", false},
		{"Sony/Nikon no underscore", "DSC01234", false},
		{"Nikon Coolpix", "DSCN0001", false},
		{"Fujifilm", "DSCF5678", false},
		{"Canon Adobe RGB", "_MG_1721", false},
		{"Sony Adobe RGB", "_DSC1234", false},
		{"Sony Adobe RGB underscore", "_DSC_9999", false},
		{"Google Pixel", "PXL_20230520", false},
		{"Canon video", "MVI_0001", false},
		{"Samsung", "SAM_0001", false},
		{"Panorama", "PANO_0001", false},
		{"Generic video", "VID_0001", false},
		{"Windows Phone", "WP_0001", false},
		{"Canon burst", "CSI_0001", false},
		{"Ricoh", "RIMG0001", false},
		{"Casio", "CIMG0001", false},
		{"Panasonic", "P1000001", false},
		{"GoPro Hero", "GOPR1234", false},
		{"GoPro Hero 8+", "GH0100001", false},
		{"DCF generic", "ABCD0001", false},

		// Insta360 multi-segment naming
		{"Insta360 image", "IMG_20240501_143000_00_001", false},
		{"Insta360 video", "VID_20240501_143000_00_005", false},

		// Not meaningful (no letters)
		{"pure digits", "20230520_143000", false},
		{"numeric only", "12345", false},

		// Edge cases
		{"empty", "", false},
		{"single letter", "a", true},
		{"underscores only", "___", false},
		{"DCIM prefix with digits", "DCIM_001", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := !cameraPattern.MatchString(tt.stem) && hasLetterPattern.MatchString(tt.stem)
			if got != tt.want {
				t.Errorf("isMeaningfulName(%q) = %v, want %v", tt.stem, got, tt.want)
			}
		})
	}
}

func TestDuplicateSuffixPattern(t *testing.T) {
	tests := []struct {
		stem string
		want bool
	}{
		{"IMG_3162 (1)", true},
		{"photo (2)", true},
		{"vacation (42)", true},
		{"sunset - Copy", true},
		{"sunset - copy", true},
		{"document copy", true},
		{"document copy 3", true},
		{"presentation Copy (1)", true},
		{"wedding_photo (1)", true},
		// No match
		{"IMG_3162", false},
		{"sunset", false},
		{"copy-of-file", false}, // "copy" as prefix, not suffix
		{"(1) at start", false}, // not at end
	}

	for _, tt := range tests {
		t.Run(tt.stem, func(t *testing.T) {
			got := duplicateSuffixPattern.MatchString(tt.stem)
			if got != tt.want {
				t.Errorf("duplicateSuffixPattern.MatchString(%q) = %v, want %v", tt.stem, got, tt.want)
			}
		})
	}
}

func TestDatePattern(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"20230520_143000.jpg", true},
		{"2023-05-20_sunset.jpg", true},
		{"2023_05_20_sunset.jpg", true},
		{"20230520_wedding.jpg", true},
		{"IMG_3162.jpg", false},
		{"sunset.jpg", false},
		{"2023-05-20.jpg", false}, // no underscore after date
		{"20230520.jpg", false},   // no underscore after 8 digits
		{"20230520", false},       // not a filename
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := datePattern.MatchString(tt.name)
			if got != tt.want {
				t.Errorf("datePattern.MatchString(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// dupe builds one candidate in a hash group. The election is a pure function
// of the paths, so stating them is the whole setup — this used to need a
// migrated SQLite database, a seeded registry, a metadata row per file and a
// writer flush to assert the same rules.
func dupe(hash, dir, name string) masterFile {
	return masterFile{FileHash: hash, FileDir: dir, FileName: name, absPath: dir + "/" + name}
}

func electedNames(rows []masterFile) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.FileName)
	}
	return out
}

func TestElectMastersKeepsOnePerHash(t *testing.T) {
	rows := []masterFile{
		dupe("a", "/card/DCIM", "IMG_1234.jpg"),
		dupe("a", "/photos/Goa Trip 2024", "IMG_1234.jpg"),
		dupe("b", "/card/DCIM", "IMG_9999.jpg"),
	}
	masters, groups := electMasters(rows)
	if len(masters) != 2 {
		t.Fatalf("elected %v, want one per hash", electedNames(masters))
	}
	if groups != 1 {
		t.Errorf("duplicate groups = %d, want 1 (only hash a has copies)", groups)
	}
	// The meaningful leaf folder earns the dir bonus; a generic one does not.
	if masters[0].FileDir != "/photos/Goa Trip 2024" {
		t.Errorf("master for hash a = %q, want the meaningfully-named folder", masters[0].FileDir)
	}
}

// Only the immediate parent may decide the dir bonus: file_dir is absolute,
// and a generic segment higher up (Photos, Users) must not disqualify a
// meaningful leaf.
func TestElectMastersDirBonusIgnoresGenericAncestors(t *testing.T) {
	rows := []masterFile{
		dupe("dup", "/Users/x/Photos/dcim", "IMG_1.jpg"),
		dupe("dup", "/Users/x/Photos/Goa Trip 2024", "IMG_1.jpg"),
	}
	masters, _ := electMasters(rows)
	if len(masters) != 1 || masters[0].FileDir != "/Users/x/Photos/Goa Trip 2024" {
		t.Errorf("elected %+v, want the meaningful leaf folder", electedNames(masters))
	}
}

// Identical score and identical path length: the (file_dir, file_name) order
// the rows arrive in decides, never insertion order, so a re-scan elects the
// same file every time.
func TestElectMastersDeterministicTieBreak(t *testing.T) {
	rows := []masterFile{
		dupe("tied", "/photos/trips/goa", "beach_a.jpg"),
		dupe("tied", "/photos/trips/goa", "beach_b.jpg"),
	}
	masters, _ := electMasters(rows)
	if len(masters) != 1 || masters[0].FileName != "beach_a.jpg" {
		t.Errorf("elected %v, want beach_a.jpg (first in name order)", electedNames(masters))
	}
}

// The returned masters keep the order they arrived in, which the clustering
// and the collision suffixes both depend on.
func TestElectMastersKeepsInputOrder(t *testing.T) {
	rows := []masterFile{
		dupe("a", "/src", "a.jpg"),
		dupe("b", "/src", "b.jpg"),
		dupe("c", "/src", "c.jpg"),
	}
	masters, groups := electMasters(rows)
	if got := electedNames(masters); got[0] != "a.jpg" || got[1] != "b.jpg" || got[2] != "c.jpg" {
		t.Errorf("order = %v, want the input order", got)
	}
	if groups != 0 {
		t.Errorf("duplicate groups = %d, want 0 — every hash is unique", groups)
	}
}
