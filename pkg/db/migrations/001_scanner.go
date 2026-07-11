// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package migrations

var schema001 = Migration{
	Version:     001,
	Description: "scanner_schema",
	SQL: []string{
		scanSessions,
		fileRegistry,
	},
}

const scanSessions = `
CREATE TABLE IF NOT EXISTS scan_sessions (
    id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at TEXT,
    status TEXT NOT NULL DEFAULT 'STARTED',

    root_paths TEXT NOT NULL,

    -- Progress tracking
    files_discovered INTEGER DEFAULT 0,
    files_skipped    INTEGER DEFAULT 0,
    files_new        INTEGER DEFAULT 0,
    files_modified   INTEGER DEFAULT 0,
    files_hashed     INTEGER DEFAULT 0,

    -- Error tracking
    errors_encountered INTEGER DEFAULT 0,
    last_error         TEXT
);`

// file_registry table with indexes
const fileRegistry = `
CREATE TABLE IF NOT EXISTS file_registry (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Physical identity
    file_path        TEXT    NOT NULL,
    file_size        INTEGER NOT NULL,
    file_modified_at TEXT    NOT NULL,

    -- Discovery metadata
    discovered_at   TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at    TEXT NOT NULL DEFAULT (datetime('now')),
    scan_session_id TEXT NOT NULL REFERENCES scan_sessions(id),
    source_root     TEXT NOT NULL,

    -- File classification
    media_type     TEXT,
    file_extension TEXT NOT NULL,

    -- Processing state machine
    scan_status TEXT NOT NULL DEFAULT 'DISCOVERED',
    
    -- Path storage
    path_type   TEXT NOT NULL DEFAULT 'RELATIVE',
    file_origin TEXT NOT NULL DEFAULT 'SOURCE',

    CHECK (media_type  IN ('IMAGE', 'VIDEO', 'SIDECAR', 'RAW', 'UNKNOWN')),
    CHECK (scan_status IN ('DISCOVERED', 'HASHING', 'HASHED', 'ANALYZING', 'ANALYZED', 'ERROR'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_file_registry_path_root ON file_registry(file_path, source_root);
CREATE INDEX IF NOT EXISTS idx_file_registry_session ON file_registry(scan_session_id);
CREATE INDEX IF NOT EXISTS idx_file_registry_status ON file_registry(scan_status);
`
