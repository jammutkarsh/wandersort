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
    file_id INTEGER REFERENCES file_registry(id) ON DELETE SET NULL,

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

    -- every file is a master by default; the scorer demotes the losers of
    -- each duplicate group, solo files are never touched
    is_master INTEGER NOT NULL DEFAULT 1,

    created_at TEXT DEFAULT ` + sqlNowDefault + `
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_file_metadata_hash_file ON file_metadata(file_hash, file_id);

CREATE TRIGGER IF NOT EXISTS trg_file_metadata_hashed
AFTER INSERT ON file_metadata
FOR EACH ROW
BEGIN
    UPDATE file_registry SET scan_status = 'HASHED' WHERE id = NEW.file_id;
END;
`
