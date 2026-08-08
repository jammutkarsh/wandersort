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

			// macOS writes these onto any volume it indexes, including the
			// exFAT/NTFS externals a photo library usually lives on. Their
			// contents are index shards (.shadow, .buckets, .offsets,
			// .indexArrays, …), none of which is media, and there are enough of
			// them to dominate the scan's warning output — 4,417 of 4,424
			// "Unsupported file type" warnings on a 107k-file run came from
			// .Spotlight-V100 alone. Skipping the directory skips the subtree.
			// Note .Trash above is the home-directory one; .Trashes is the
			// per-volume one an external drive carries, and is a different name.
			".Spotlight-V100":         true,
			".fseventsd":              true,
			".DocumentRevisions-V100": true,
			".TemporaryItems":         true,
			".Trashes":                true,
		},
	}
}

// ClassifyName combines ignore and media checks so callers make one decision.
func (fc *FileClassifier) ClassifyName(name string) (mediaType string, shouldProcess bool, shouldIgnore bool) {
	base := filepath.Base(name)

	if fc.ignoredFiles[base] {
		return MediaTypeUnknown, false, true
	}

	// AppleDouble sidecars: copying an APFS/HFS+ file to a filesystem with no
	// native resource forks (exFAT, FAT32, NTFS, SMB) makes macOS write the
	// fork and Finder metadata to a companion "._<name>" file. It carries the
	// shadowed file's extension, so the media check below would otherwise admit
	// it as a photo. They are also byte-identical to each other whenever the
	// original had no resource fork, which collapses them into one enormous
	// bogus duplicate group — 10,174 of them in a single group on a 107k-file
	// run. Name is enough to identify them; the AppleDouble magic (0x00051607)
	// would confirm it but costs an open+read per candidate, which is the
	// expense this check exists to avoid.
	if strings.HasPrefix(base, "._") {
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

func (fc *FileClassifier) ShouldIgnoreDir(name string) bool {
	return fc.ignoredDirs[name]
}

var (
	genericDirs = map[string]bool{
		"dcim": true, "camera": true, "photos": true, "temp": true,
		"downloads": true, "desktop": true, "backup": true, "misc": true,
	}

	// no (?i): IsGenericDirName lowercases first, and case-folding made the
	// matcher backtrack through unicode.SimpleFold on every call
	genericDirPattern = regexp.MustCompile(`^(\.?(thumbnails|trashed.*|sync|cache))$` +
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
