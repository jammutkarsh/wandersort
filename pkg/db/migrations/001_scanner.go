// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package migrations

var schema001 = Migration{
	Version:     0o01,
	Description: "scanner_schema",
	SQL: []string{
		fileRegistry,
	},
}

// file_registry table with indexes
const fileRegistry = `
CREATE TABLE IF NOT EXISTS file_registry (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Physical identity: absolute directory + name. One row per file on disk,
    -- no matter which scan root the file was discovered through
    file_dir         TEXT    NOT NULL,
    file_name        TEXT    NOT NULL,
    file_size        INTEGER NOT NULL,
    file_modified_at TEXT    NOT NULL,

    -- Volume the file lives on; lets a future re-anchor pass rewrite paths
    -- when an external drive remounts elsewhere. NULL when unresolvable
    volume_uuid TEXT,

    -- Discovery metadata
    discovered_at TEXT NOT NULL,
    last_seen_at  TEXT NOT NULL,

    -- File classification
    media_type     TEXT,
    file_extension TEXT NOT NULL,

    -- Processing state machine
    scan_status TEXT NOT NULL DEFAULT 'DISCOVERED',

    file_origin TEXT NOT NULL DEFAULT 'SOURCE',

    -- Set once execute lands this file at its target, copy or move alike.
    -- A fact about the file, not the plan: virtual_fs_entries rows are
    -- replaced on every scan and settings change, this flag never is. The
    -- scorer treats a placed file as the permanent master of its hash, and
    -- the vfs phase never re-proposes or deletes its plan row (spec D10/D11)
    placed INTEGER NOT NULL DEFAULT 0,

    CHECK (media_type  IN ('IMAGE', 'VIDEO', 'SIDECAR', 'RAW', 'UNKNOWN')),
    CHECK (scan_status IN ('DISCOVERED', 'ANALYZING', 'ANALYZED', 'ERROR'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_file_registry_dir_name ON file_registry(file_dir, file_name);
CREATE INDEX IF NOT EXISTS idx_file_registry_status ON file_registry(scan_status);
`
