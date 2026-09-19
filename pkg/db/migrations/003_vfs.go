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
		folderNodes,
		virtualFSEntries,
		userLabels,
	},
}

// folder_nodes is the plan's folder tree (spec D12): a folder keeps its id
// when it is renamed or moved, so an edit can name it. A folder's path is its
// ancestors' names joined. A folder no file uses any more is deleted outright:
// AUTOINCREMENT never hands its id out again, so a stale reference can only
// miss, never land on a different folder.
const folderNodes = `
CREATE TABLE IF NOT EXISTS folder_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    parent_id INTEGER REFERENCES folder_nodes(id),
    name TEXT NOT NULL,
    -- the grouping level that made this folder: year, month, screenshots,
    -- fallback, orphan, or one of the rules levels (date, location, …)
    level TEXT NOT NULL,
    -- what files this folder holds (spec D13), a JSON array of alternatives,
    -- each an AND of levels: [{"date":[3],"location":["Goa"]},
    -- {"date":[20],"location":["Manali"]}]. A file belongs if it matches any
    -- alternative; [] matches nothing, [{}] everything. A folder's full range
    -- is its bounds AND its ancestors'. Read in Go only (vfs.Bounds); nothing
    -- queries inside it.
    bounds TEXT NOT NULL DEFAULT '[{}]'
);

CREATE INDEX IF NOT EXISTS idx_folder_nodes_parent ON folder_nodes(parent_id);
`

// virtual_fs_entries holds the proposed destination for every master file of a
// session. The VFS phase writes PROPOSED rows; the review flow flips them to
// APPROVED; the Execute phase marks DONE/ERROR.
const virtualFSEntries = `
CREATE TABLE IF NOT EXISTS virtual_fs_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id INTEGER NOT NULL REFERENCES file_registry(id),
    source_path TEXT NOT NULL,
    -- the folder the file goes in. target_path repeats that folder's path
    -- plus the file name, kept in step by the planner and the review save.
    node_id INTEGER NOT NULL REFERENCES folder_nodes(id),
    target_path TEXT NOT NULL,
    cluster_id TEXT,
    status TEXT NOT NULL DEFAULT 'PROPOSED'
        CHECK (status IN ('PROPOSED','APPROVED','DONE','ERROR')),
    -- the folder the location level made for this file, so the review tree
    -- can hang the file's GPS off it (any rules order puts it at a different
    -- depth). NULL when the file has no location folder — or no longer has
    -- one: a review merge can move the file out from under it, and the
    -- emptied place folder is then deleted.
    location_node_id INTEGER REFERENCES folder_nodes(id) ON DELETE SET NULL,
    -- why an ERROR row failed. The log line has the same text, but a phase
    -- that moves the user's files needs "which ones failed and why" to be a
    -- query, not a grep. NULL for every other status.
    error TEXT,
    created_at TEXT NOT NULL DEFAULT ` + sqlNowDefault + `
);

-- one proposal row per file, ever — the VFS phase always wholesale-replaces
-- the whole table, so there is never more than one live batch to disambiguate
CREATE UNIQUE INDEX IF NOT EXISTS idx_vfs_file ON virtual_fs_entries(file_id);
CREATE INDEX IF NOT EXISTS idx_vfs_status ON virtual_fs_entries(status);
CREATE INDEX IF NOT EXISTS idx_vfs_node ON virtual_fs_entries(node_id);
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
