// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scanner

import (
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/classifier"
)

type FileRegistry struct {
	ID             int64     `db:"id"`
	FileDir        string    `db:"file_dir"`
	FileName       string    `db:"file_name"`
	FileSize       int64     `db:"file_size"`
	FileModifiedAt time.Time `db:"file_modified_at"`

	VolumeUUID *string `db:"volume_uuid"`

	DiscoveredAt  time.Time  `db:"discovered_at"`
	LastSeenAt    time.Time  `db:"last_seen_at"`
	ScanSessionID uuid.UUID  `db:"scan_session_id"`
	DeletedAt     *time.Time `db:"deleted_at"`

	MediaType     string `db:"media_type"`
	FileExtension string `db:"file_extension"`
	ScanStatus    string `db:"scan_status"`

	FileOrigin string `db:"file_origin" json:"fileOrigin"`
}

// File origin constants
const (
	FileOriginSource    = "SOURCE"
	FileOriginOrganized = "ORGANIZED"
	FileOriginUnknown   = "UNKNOWN"
)

// AbsolutePath returns the full absolute path of the file
func (fr *FileRegistry) AbsolutePath() string {
	return filepath.Join(fr.FileDir, fr.FileName)
}

// IsPrimarySource reports whether this registry entry is an original/canonical file
// RAW files from a DSLR that has no paired JPG are still primary sources
func (fr *FileRegistry) IsPrimarySource() bool {
	switch fr.MediaType {
	case classifier.MediaTypeImage, classifier.MediaTypeRaw, classifier.MediaTypeVideo:
		return true
	default:
		return false
	}
}

// NeedsTranscoding reports whether this file must be decoded on the fly before
// being passed to downstream consumers such as AI inference pipelines
// RAW images cannot be used directly and must be converted first
func (fr *FileRegistry) NeedsTranscoding() bool {
	return fr.MediaType == classifier.MediaTypeRaw
}

// FileDiscovery is the lightweight struct used during directory walking.
// Dir is the file's absolute parent directory
type FileDiscovery struct {
	ID         int64
	Dir        string
	Name       string
	Size       int64
	ModTime    time.Time
	Extension  string
	VolumeUUID string
	MediaType  string
}

type ScanSession struct {
	ID          uuid.UUID  `db:"id"`
	StartedAt   time.Time  `db:"started_at"`
	CompletedAt *time.Time `db:"completed_at"`
	Status      string     `db:"status"`

	RootPaths []string `db:"root_paths"` // JSONB in DB

	FilesDiscovered int `db:"files_discovered"`
	FilesSkipped    int `db:"files_skipped"`
	FilesNew        int `db:"files_new"`
	FilesModified   int `db:"files_modified"`

	ErrorsEncountered int     `db:"errors_encountered"`
	LastError         *string `db:"last_error"`
}
