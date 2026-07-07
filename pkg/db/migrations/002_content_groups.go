package migrations

var schema002 = Migration{
	Version:     002,
	Description: "content_groups_schema",
	SQL: []string{
		contentGroups,
		contentGroupMembers,
	},
}

const contentGroups = `
CREATE TABLE IF NOT EXISTS content_groups (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    content_hash TEXT UNIQUE NOT NULL,
    master_file_id INTEGER REFERENCES file_registry(id) ON DELETE SET NULL,
    total_copies INTEGER DEFAULT 1,

    exif_image_width        INTEGER,
    exif_image_height       INTEGER,
    exif_gps_latitude       REAL,
    exif_gps_longitude      REAL,
    exif_make               TEXT,
    exif_model              TEXT,
    exif_date_time_original TEXT,
    exif_create_date        TEXT,

    created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_content_groups_hash   ON content_groups(content_hash);
CREATE INDEX IF NOT EXISTS idx_content_groups_master ON content_groups(master_file_id)
    WHERE master_file_id IS NOT NULL;
`

const contentGroupMembers = `
CREATE TABLE IF NOT EXISTS content_group_members (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id INTEGER NOT NULL REFERENCES content_groups(id) ON DELETE CASCADE,
    file_id  INTEGER NOT NULL REFERENCES file_registry(id)  ON DELETE CASCADE,

    is_master      INTEGER DEFAULT 0,
    metadata_score INTEGER DEFAULT 0,

    created_at TEXT DEFAULT (datetime('now')),

    UNIQUE(group_id, file_id)
);

CREATE INDEX IF NOT EXISTS idx_content_group_members_group  ON content_group_members(group_id);
CREATE INDEX IF NOT EXISTS idx_content_group_members_file   ON content_group_members(file_id);
CREATE INDEX IF NOT EXISTS idx_content_group_members_master ON content_group_members(group_id, is_master)
    WHERE is_master = 1;
`
