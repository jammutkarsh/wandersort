package migrations

var schema003 = Migration{
	Version:     0o03,
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
    session_id TEXT NOT NULL REFERENCES scan_sessions(id),
    file_id INTEGER NOT NULL REFERENCES file_registry(id),
    source_path TEXT NOT NULL,
    target_path TEXT NOT NULL,
    cluster_id TEXT,
    status TEXT NOT NULL DEFAULT 'PROPOSED'
        CHECK (status IN ('PROPOSED','APPROVED','DONE','ERROR')),
    suggestion TEXT,
    suggestion_source TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_vfs_session_file ON virtual_fs_entries(session_id, file_id);
CREATE INDEX IF NOT EXISTS idx_vfs_session ON virtual_fs_entries(session_id);
CREATE INDEX IF NOT EXISTS idx_vfs_status ON virtual_fs_entries(status);
`

// user_labels remembers confirmed folder names (events) and anchor locations
// (home/work). Written by the review flow, read by the VFS phase to rank
// suggestions on later scans.
const userLabels = `
CREATE TABLE IF NOT EXISTS user_labels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    label TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('EVENT','ANCHOR_HOME','ANCHOR_WORK')),
    time_start TEXT,
    time_end TEXT,
    gps_lat REAL,
    gps_lon REAL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
`
