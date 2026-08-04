// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package migrations

var schema003 = Migration{
	Version:     3,
	Description: "vfs_schema",
	SQL: []string{
		virtualFSEntries,
		userLabels,
	},
}

// virtual_fs_entries holds the proposed destination for every master file of a
// session. The VFS phase writes PROPOSED rows; the review flow flips them to
// APPROVED; the Execute phase marks DONE/ERROR.
const virtualFSEntries = `
CREATE TABLE IF NOT EXISTS virtual_fs_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id INTEGER NOT NULL REFERENCES file_registry(id),
    source_path TEXT NOT NULL,
    target_path TEXT NOT NULL,
    cluster_id TEXT,
    status TEXT NOT NULL DEFAULT 'PROPOSED'
        CHECK (status IN ('PROPOSED','APPROVED','DONE','ERROR')),
    -- the folder the location level emitted for this file. The VFS build
    -- records it so the review tree can hang the file's GPS off that node by
    -- path instead of guessing a depth (which broke as soon as location wasn't
    -- the first rules level).
    location_dir TEXT,
    created_at TEXT NOT NULL DEFAULT ` + sqlNowDefault + `
);

-- one proposal row per file, ever — the VFS phase always wholesale-replaces
-- the whole table, so there is never more than one live batch to disambiguate
CREATE UNIQUE INDEX IF NOT EXISTS idx_vfs_file ON virtual_fs_entries(file_id);
CREATE INDEX IF NOT EXISTS idx_vfs_status ON virtual_fs_entries(status);
-- lets Confirm's "SELECT DISTINCT target_path" (no WHERE) walk a sorted
-- index instead of a full table scan + temp b-tree for DISTINCT
CREATE INDEX IF NOT EXISTS idx_vfs_target_path ON virtual_fs_entries(target_path);
`

// user_labels remembers the folder names the reviewer typed. Written by the
// review flow, read back as rename completions in later reviews.
// SAVED_PLACE is a legacy kind: anchors are built from config.yaml in memory
// now, so nothing writes it any more — the CHECK still allows it so rows
// written by older versions stay valid.
const userLabels = `
CREATE TABLE IF NOT EXISTS user_labels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    label TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('EVENT','SAVED_PLACE')),
    time_start TEXT,
    time_end TEXT,
    gps_lat REAL,
    gps_lon REAL,
    created_at TEXT NOT NULL DEFAULT ` + sqlNowDefault + `
);
`
