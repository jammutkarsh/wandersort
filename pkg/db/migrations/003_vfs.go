package migrations

var schema003 = Migration{
	Version:     3,
	Description: "vfs_schema",
	SQL: []string{
		folderNodes,
		virtualFSEntries,
		userLabels,
		librarySettings,
	},
}

// library_settings holds the library's folder settings: one row, travelling
// with the library. No row means defaults.
const librarySettings = `
CREATE TABLE IF NOT EXISTS library_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    -- config.Settings as JSON; read in Go only
    settings TEXT NOT NULL
) STRICT;
`

// folder_nodes is the plan's folder tree: a folder keeps its id across renames
// and moves; its path is its ancestors' names. AUTOINCREMENT never reuses an
// id, so a stale reference can only miss.
const folderNodes = `
CREATE TABLE IF NOT EXISTS folder_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    parent_id INTEGER REFERENCES folder_nodes(id),
    name TEXT NOT NULL,
    -- the grouping level that made this folder: year, month, screenshots,
    -- fallback, orphan, or one of the rules levels (date, location, …)
    level TEXT NOT NULL,
    -- what files this folder holds: a JSON array of alternatives, each an
    -- AND of levels: [{"date":[3],"location":["<city A>"]},
    -- {"date":[20],"location":["<city B>"]}]. A file belongs if it matches any
    -- alternative; [] matches nothing, [{}] everything. A folder's full range
    -- is its bounds AND its ancestors'. Read in Go only (vfs.Bounds); nothing
    -- queries inside it.
    bounds TEXT NOT NULL DEFAULT '[{}]'
) STRICT;

CREATE INDEX IF NOT EXISTS idx_folder_nodes_parent ON folder_nodes(parent_id);
`

// virtual_fs_entries holds every master file's planned destination. No state
// column: pending while unplaced with no TRANSFER error, failed with one,
// placed once file_registry.placed is set.
const virtualFSEntries = `
CREATE TABLE IF NOT EXISTS virtual_fs_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id INTEGER NOT NULL REFERENCES file_registry(id) ON DELETE CASCADE,
    source_path TEXT NOT NULL,
    -- the folder the file goes in. target_path repeats that folder's path
    -- plus the file name, kept in step by the planner and the review save.
    node_id INTEGER NOT NULL REFERENCES folder_nodes(id),
    target_path TEXT NOT NULL,
    cluster_id TEXT,
    -- the folder the location level made for this file, so the review tree
    -- can hang the file's GPS off it (any rules order puts it at a different
    -- depth). NULL when the file has no location folder — or no longer has
    -- one: a review merge can move the file out from under it, and the
    -- emptied place folder is then deleted.
    location_node_id INTEGER REFERENCES folder_nodes(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL DEFAULT ` + sqlNowDefault + `
) STRICT;

-- one proposal row per file, ever — the VFS phase always wholesale-replaces
-- the whole table, so there is never more than one live batch to disambiguate
CREATE UNIQUE INDEX IF NOT EXISTS idx_vfs_file ON virtual_fs_entries(file_id);
CREATE INDEX IF NOT EXISTS idx_vfs_node ON virtual_fs_entries(node_id);
`

// user_labels remembers folder names typed in review, for rename completion.
// SAVED_PLACE is legacy: nothing writes it, the CHECK still allows old rows.
const userLabels = `
CREATE TABLE IF NOT EXISTS user_labels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    label TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('EVENT','SAVED_PLACE')),
    time_start TEXT,
    time_end TEXT,
    gps_lat REAL,
    gps_lon REAL,
    created_at TEXT NOT NULL DEFAULT ` + sqlNowDefault + `,
    -- a set of names, not a log: every review save offers each renamed
    -- folder again, and a name already here is not a new one
    UNIQUE (label, kind)
) STRICT;
`
