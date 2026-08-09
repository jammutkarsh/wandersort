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

    -- Soft delete: stamped when a clean scan of the file's root no longer
    -- sees it, cleared if the file reappears, hard-purged after retention
    deleted_at TEXT,

    -- File classification
    media_type     TEXT,
    file_extension TEXT NOT NULL,

    -- Processing state machine
    scan_status TEXT NOT NULL DEFAULT 'DISCOVERED',

    file_origin TEXT NOT NULL DEFAULT 'SOURCE',

    CHECK (media_type  IN ('IMAGE', 'VIDEO', 'SIDECAR', 'RAW', 'UNKNOWN')),
    CHECK (scan_status IN ('DISCOVERED', 'ANALYZING', 'ANALYZED', 'ERROR'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_file_registry_dir_name ON file_registry(file_dir, file_name);
CREATE INDEX IF NOT EXISTS idx_file_registry_status ON file_registry(scan_status);
-- Partial over deleted rows only, for purgeExpired's cutoff scan. Live-row
-- filters (deleted_at IS NULL) match the vast majority of rows, so an index
-- would not beat the table scan there — deliberate, not an oversight
CREATE INDEX IF NOT EXISTS idx_file_registry_deleted ON file_registry(deleted_at) WHERE deleted_at IS NOT NULL;

-- The single definition of "live": every read query goes through this view
-- instead of hand-writing deleted_at IS NULL (UPDATEs still hit the table)
CREATE VIEW IF NOT EXISTS live_files AS
    SELECT * FROM file_registry WHERE deleted_at IS NULL;
`
