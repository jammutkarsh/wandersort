// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package classifier

import (
	"testing"
)

// ---------------------------------------------------------------------------
// ClassifyName
// ---------------------------------------------------------------------------

func TestClassifyName(t *testing.T) {
	fc := NewFileClassifier()

	tests := []struct {
		path          string
		wantType      string
		wantProcessed bool
		wantIgnored   bool
	}{
		// Images
		{"photo.jpg", MediaTypeImage, true, false},
		{"photo.JPEG", MediaTypeImage, true, false},
		{"photo.png", MediaTypeImage, true, false},
		{"photo.bmp", MediaTypeImage, true, false},
		{"photo.heic", MediaTypeImage, true, false},
		{"photo.HEIC", MediaTypeImage, true, false},
		{"photo.webp", MediaTypeImage, true, false},

		// Videos
		{"video.mp4", MediaTypeVideo, true, false},
		{"video.MP4", MediaTypeVideo, true, false},
		{"video.mov", MediaTypeVideo, true, false},
		{"video.MOV", MediaTypeVideo, true, false},

		// RAW
		{"raw.cr2", MediaTypeRaw, true, false},
		{"raw.CR2", MediaTypeRaw, true, false},
		{"raw.dng", MediaTypeRaw, true, false},
		{"raw.DNG", MediaTypeRaw, true, false},

		// Sidecar
		{"sidecar.aae", MediaTypeSidecar, true, false},
		{"sidecar.AAE", MediaTypeSidecar, true, false},

		// Ignored
		{".DS_Store", MediaTypeUnknown, false, true},
		{"Thumbs.db", MediaTypeUnknown, false, true},
		{"/Volumes/Backups/Pictures/.DS_Store", MediaTypeUnknown, false, true},

		// AppleDouble sidecars: they carry the shadowed file's extension, so
		// every one of these would classify as real media without the prefix rule
		{"._IMG_20180106_211920.jpg", MediaTypeUnknown, false, true},
		{"._photo.HEIC", MediaTypeUnknown, false, true},
		{"._raw.cr2", MediaTypeUnknown, false, true},
		{"._clip.mov", MediaTypeUnknown, false, true},
		{"._sidecar.aae", MediaTypeUnknown, false, true},
		{"/Volumes/Backups/Family/._IMG_0001.jpg", MediaTypeUnknown, false, true},
		{"._", MediaTypeUnknown, false, true},
		// a leading dot alone is not AppleDouble, and a "._" anywhere but the
		// start is an ordinary filename
		{".hidden.jpg", MediaTypeImage, true, false},
		{"my._photo.jpg", MediaTypeImage, true, false},

		// Unsupported
		{"readme.txt", MediaTypeUnknown, false, false},
		{"script.py", MediaTypeUnknown, false, false},
		{"Makefile", MediaTypeUnknown, false, false},
		{"archive.zip", MediaTypeUnknown, false, false},
		{"", MediaTypeUnknown, false, false},

		// Path with directory components
		{"/home/user/Photos/2023/IMG_001.jpg", MediaTypeImage, true, false},
		{"~/Pictures/vacation.HEIC", MediaTypeImage, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			mediaType, processed, ignored := fc.ClassifyName(tt.path)
			if mediaType != tt.wantType || processed != tt.wantProcessed || ignored != tt.wantIgnored {
				t.Errorf("ClassifyName(%q) = (%q, %v, %v), want (%q, %v, %v)",
					tt.path, mediaType, processed, ignored, tt.wantType, tt.wantProcessed, tt.wantIgnored)
			}
		})
	}
}

func TestShouldIgnoreDir(t *testing.T) {
	fc := NewFileClassifier()

	tests := []struct {
		dir  string
		want bool
	}{
		{".git", true},
		{".svn", true},
		{"node_modules", true},
		{".Trash", true},
		{"$RECYCLE.BIN", true},
		{"System Volume Information", true},

		// macOS volume metadata — these hold the index shards that produced
		// almost every "Unsupported file type" warning on the 107k-file run
		{".Spotlight-V100", true},
		{".fseventsd", true},
		{".DocumentRevisions-V100", true},
		{".TemporaryItems", true},
		{".Trashes", true},

		// real directories a library actually uses
		{"DCIM", false},
		{"Camera", false},
		{"WhatsApp Images", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			if got := fc.ShouldIgnoreDir(tt.dir); got != tt.want {
				t.Errorf("ShouldIgnoreDir(%q) = %v, want %v", tt.dir, got, tt.want)
			}
		})
	}
}

func TestIsGenericDirName(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		want bool
	}{
		{"dcim", "dcim", true},
		{"DCIM uppercase", "DCIM", true},
		{"backup", "backup", true},
		{"downloads", "Downloads", true},
		{"desktop", "Desktop", true},
		{"misc", "misc", true},
		{"temp", "temp", true},
		{"photos", "photos", true},
		{"camera", "camera", true},
		{"sync", "sync", true},
		{"cache", "cache", true},
		{"WhatsApp Images", "WhatsApp Images", true},
		{"Telegram media", "Telegram Images", true},
		{"Signal videos", "Signal Media", true},
		{"New Folder", "New Folder", true},
		{"new folder (2)", "New Folder (2)", true},
		{"new folder with spaces", "New Folder (5)", true},
		{"old backup", "old backup", true},
		{"old backup num", "old backup 3", true},
		{"backup 2023", "backup_2023", true},
		{"dcim variant", "DCIM 1", true},
		{"temp variant", "tmp_123", true},
		{".thumbnails", ".thumbnails", true},
		{"trashed", "Trashed documents", true},
		{"trips", "trips", false},
		{"goa", "goa", false},
		{"year", "2024", false},
		{"wedding", "wedding", false},
		{"family", "family", false},
		{"empty name", "", false},
		{"root", "/", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsGenericDirName(tt.dir)
			if got != tt.want {
				t.Errorf("IsGenericDirName(%q) = %v, want %v", tt.dir, got, tt.want)
			}
		})
	}
}
