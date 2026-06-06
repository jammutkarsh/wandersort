package migrations

import (
	"fmt"
	"strings"

	sm "github.com/jammutkarsh/wandersort/pkg/statusmanager"
)

var schema001 = Migration{
	Version:     001,
	Description: "scanner_schema",
	SQL: []string{
		scanSessions,
		fileRegistry,
	},
}

// scan_sessions table with indexes
var scanSessions = fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS scan_sessions (
    id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at TEXT,
    status TEXT NOT NULL DEFAULT '%s',

    root_paths TEXT NOT NULL,

    -- Progress tracking
    files_discovered INTEGER DEFAULT 0,
    files_skipped    INTEGER DEFAULT 0,
    files_new        INTEGER DEFAULT 0,
    files_modified   INTEGER DEFAULT 0,
    files_hashed     INTEGER DEFAULT 0,

    -- Error tracking
    errors_encountered INTEGER DEFAULT 0,
    last_error         TEXT,

    CHECK (status IN (%s))
);

CREATE INDEX IF NOT EXISTS idx_scan_sessions_status  ON scan_sessions(status);
CREATE INDEX IF NOT EXISTS idx_scan_sessions_started ON scan_sessions(started_at DESC);
`, sm.WorkflowStatusStarted, quotedStatuses())

// file_registry table with indexes
const fileRegistry = `
CREATE TABLE IF NOT EXISTS file_registry (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Physical identity
    file_path        TEXT    NOT NULL,
    file_size        INTEGER NOT NULL,
    file_modified_at TEXT    NOT NULL,

    -- Hash (populated in hashing phase)
    file_hash TEXT,

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

    -- Capture grouping
    capture_stem TEXT,
    capture_role TEXT,

    -- Timestamps
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),

    CHECK (media_type  IN ('IMAGE', 'VIDEO', 'SIDECAR', 'RAW', 'UNKNOWN')),
    CHECK (scan_status IN ('DISCOVERED', 'HASHING', 'HASHED', 'ANALYZING', 'ANALYZED', 'ERROR'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_file_registry_path_root ON file_registry(file_path, source_root);
CREATE INDEX IF NOT EXISTS idx_file_registry_hash       ON file_registry(file_hash) WHERE file_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_file_registry_session     ON file_registry(scan_session_id);
CREATE INDEX IF NOT EXISTS idx_file_registry_status      ON file_registry(scan_status);
CREATE INDEX IF NOT EXISTS idx_file_registry_source_root ON file_registry(source_root);
CREATE INDEX IF NOT EXISTS idx_file_registry_media_type  ON file_registry(media_type);
CREATE INDEX IF NOT EXISTS idx_file_registry_origin      ON file_registry(file_origin);
CREATE INDEX IF NOT EXISTS idx_file_registry_capture     ON file_registry(capture_stem, source_root)
    WHERE capture_stem IS NOT NULL;
`

func quotedStatuses() string {
	statuses := []string{
		sm.WorkflowStatusStarted,
		sm.WorkflowStatusScanning,
		sm.WorkflowStatusScanned,
		sm.WorkflowStatusHashing,
		sm.WorkflowStatusHashed,
		sm.WorkflowStatusCompleted,
		sm.WorkflowStatusFailed,
		sm.WorkflowStatusCancelled,
	}
	quoted := make([]string, 0, len(statuses))
	for _, status := range statuses {
		quoted = append(quoted, fmt.Sprintf("'%s'", status))
	}
	return strings.Join(quoted, ", ")
}
