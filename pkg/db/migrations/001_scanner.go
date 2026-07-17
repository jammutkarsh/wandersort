package migrations

var schema001 = Migration{
	Version:     0o01,
	Description: "scanner_schema",
	SQL: []string{
		scanSessions,
		fileRegistry,
	},
}

const scanSessions = `
CREATE TABLE IF NOT EXISTS scan_sessions (
    id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z','now')),
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
    discovered_at   TEXT NOT NULL,
    last_seen_at    TEXT NOT NULL,
    scan_session_id TEXT NOT NULL REFERENCES scan_sessions(id),

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
    CHECK (scan_status IN ('DISCOVERED', 'HASHING', 'HASHED', 'ANALYZING', 'ANALYZED', 'ERROR'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_file_registry_dir_name ON file_registry(file_dir, file_name);
CREATE INDEX IF NOT EXISTS idx_file_registry_session ON file_registry(scan_session_id);
CREATE INDEX IF NOT EXISTS idx_file_registry_status ON file_registry(scan_status);
CREATE INDEX IF NOT EXISTS idx_file_registry_deleted ON file_registry(deleted_at) WHERE deleted_at IS NOT NULL;
`
