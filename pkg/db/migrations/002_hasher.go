package migrations

const hasherSQL = `
-- Track hashed file count on scan sessions
ALTER TABLE scan_sessions ADD COLUMN files_hashed INTEGER DEFAULT 0;

-- Content groups (one per unique hash)
CREATE TABLE IF NOT EXISTS content_groups (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    content_hash TEXT UNIQUE NOT NULL,
    master_file_id INTEGER REFERENCES file_registry(id) ON DELETE SET NULL,
    total_copies INTEGER DEFAULT 1,

    exif_metadata TEXT,

    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

-- Membership table
CREATE TABLE IF NOT EXISTS content_group_members (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id INTEGER NOT NULL REFERENCES content_groups(id) ON DELETE CASCADE,
    file_id  INTEGER NOT NULL REFERENCES file_registry(id)  ON DELETE CASCADE,

    is_master      INTEGER DEFAULT 0,
    metadata_score INTEGER DEFAULT 0,

    created_at TEXT DEFAULT (datetime('now')),

    UNIQUE(group_id, file_id)
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_content_groups_hash   ON content_groups(content_hash);
CREATE INDEX IF NOT EXISTS idx_content_groups_master ON content_groups(master_file_id)
    WHERE master_file_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_content_group_members_group  ON content_group_members(group_id);
CREATE INDEX IF NOT EXISTS idx_content_group_members_file   ON content_group_members(file_id);
CREATE INDEX IF NOT EXISTS idx_content_group_members_master ON content_group_members(group_id, is_master)
    WHERE is_master = 1;

-- Trigger: auto-update updated_at on content_groups changes
CREATE TRIGGER IF NOT EXISTS update_content_groups_updated_at
    AFTER UPDATE ON content_groups
    FOR EACH ROW
BEGIN
    UPDATE content_groups SET updated_at = datetime('now') WHERE id = OLD.id;
END;
`
