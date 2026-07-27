// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package classifier

import (
	"path/filepath"
	"regexp"
	"strings"
)

// FileClassifier determines file types and filters
type FileClassifier struct {
	imageExtensions   map[string]bool
	videoExtensions   map[string]bool
	rawExtensions     map[string]bool
	sidecarExtensions map[string]bool
	ignoredFiles      map[string]bool
	ignoredDirs       map[string]bool
}

// NewFileClassifier creates a new classifier with predefined rules
func NewFileClassifier() *FileClassifier {
	return &FileClassifier{
		imageExtensions: map[string]bool{
			".jpg":  true,
			".jpeg": true,
			".png":  true,
			".bmp":  true,
			".heic": true,
			".webp": true,
		},
		videoExtensions: map[string]bool{
			".mp4": true,
			".mov": true,
		},
		rawExtensions: map[string]bool{
			".cr2": true, // Canon
			".dng": true, // Adobe/Universal
		},
		sidecarExtensions: map[string]bool{
			".aae": true, // iPhone edit sidecar
		},
		ignoredFiles: map[string]bool{
			".DS_Store":   true,
			"Thumbs.db":   true,
			"desktop.ini": true,
			".picasa.ini": true,
			".nomedia":    true,
		},
		ignoredDirs: map[string]bool{
			".git":                      true,
			".svn":                      true,
			"node_modules":              true,
			".Trash":                    true,
			"$RECYCLE.BIN":              true,
			"System Volume Information": true,
		},
	}
}

// ClassifyName classifies a file name and reports if it should be ignored
// This combines ignore and media checks so callers can make a single decision
func (fc *FileClassifier) ClassifyName(name string) (mediaType string, shouldProcess bool, shouldIgnore bool) {
	if fc.ignoredFiles[name] {
		return MediaTypeUnknown, false, true
	}

	ext := strings.ToLower(filepath.Ext(name))

	switch {
	case fc.imageExtensions[ext]:
		return MediaTypeImage, true, false
	case fc.videoExtensions[ext]:
		return MediaTypeVideo, true, false
	case fc.rawExtensions[ext]:
		return MediaTypeRaw, true, false
	case fc.sidecarExtensions[ext]:
		return MediaTypeSidecar, true, false
	default:
		return MediaTypeUnknown, false, false
	}
}

// ShouldIgnoreDir checks if a directory should be skipped entirely
func (fc *FileClassifier) ShouldIgnoreDir(name string) bool {
	return fc.ignoredDirs[name]
}

var (
	genericDirs = map[string]bool{
		"dcim": true, "camera": true, "photos": true, "temp": true,
		"downloads": true, "desktop": true, "backup": true, "misc": true,
	}

	genericDirPattern = regexp.MustCompile(`(?i)^(\.?(thumbnails|trashed.*|sync|cache))$` +
		`|^new folder(\s*\(\d+\))?$` +
		`|^(old\s+)?backup[\s_-]*\d*$` +
		`|^(dcim|temp|tmp|misc|downloads?)[\s_-]*\d*$` +
		`|^(whatsapp|telegram|signal)[\s_-]*(images?|videos?|media)$`)
)

// IsGenericDirName reports whether a single folder name — one path segment,
// e.g. filepath.Base of a dir, never a full path — is a known or
// pattern-matched low-signal name (DCIM, Backup, temp, etc).
func IsGenericDirName(name string) bool {
	seg := strings.ToLower(name)
	return genericDirs[seg] || genericDirPattern.MatchString(seg)
}
