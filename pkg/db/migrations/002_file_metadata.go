// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package migrations

var schema002 = Migration{
	Version:     0o02,
	Description: "file_metadata_schema",
	SQL: []string{
		fileMetadata,
	},
}

const fileMetadata = `
CREATE TABLE IF NOT EXISTS file_metadata (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_hash TEXT NOT NULL,
    -- one metadata row per file: a second one would list the file twice in
    -- every join, so execute would transfer it twice and a re-plan would
    -- fail on the one-plan-per-file index
    file_id INTEGER NOT NULL UNIQUE REFERENCES file_registry(id) ON DELETE CASCADE,

    exif_image_width        INTEGER,
    exif_image_height       INTEGER,
    -- EXIF orientation 1-8; 5-8 mean the pixels are stored rotated 90°/270°
    exif_orientation        INTEGER,
    exif_gps_latitude       REAL,
    exif_gps_longitude      REAL,
    exif_make               TEXT,
    exif_model              TEXT,
    exif_date_time_original TEXT,
    exif_create_date        TEXT,
    -- QuickTime's composite CreationDate (iOS videos) — the one capture-time
    -- tag that carries its own UTC offset. exif_create_date for a video is
    -- QuickTime's raw (UTC) CreateDate with no offset attached, which reads
    -- hours off from sibling photos' local-time exif_date_time_original;
    -- this is what actually lines a video's wall-clock time back up with them
    exif_creation_date      TEXT,
    -- QuickTime's track-level MediaCreateDate; lower priority than
    -- exif_create_date, only used when the latter is missing/bogus (e.g. an
    -- epoch/pre-1970 CreateDate some devices write)
    exif_media_create_date  TEXT,

    -- macOS/iOS stamp both Description and UserComment with the literal
    -- string "Screenshot" on a screen capture; a real camera/phone photo
    -- never carries either, so this doubles as the screenshot detector
    is_screenshot INTEGER NOT NULL DEFAULT 0,

    created_at TEXT DEFAULT ` + sqlNowDefault + `
) STRICT;

-- file_id's UNIQUE is its own index; this one serves duplicate grouping and
-- every hash join (elect, execute's duplicate cleanup)
CREATE INDEX IF NOT EXISTS idx_file_metadata_hash ON file_metadata(file_hash);
`
