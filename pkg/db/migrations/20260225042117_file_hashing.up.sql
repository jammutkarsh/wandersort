-- Add file hashing and content grouping
ALTER TABLE scan_sessions ADD COLUMN files_hashed BIGINT DEFAULT 0;

-- Content groups (one per unique hash)
CREATE TABLE IF NOT EXISTS content_groups (
    id BIGSERIAL PRIMARY KEY,
    content_hash CHAR(64) UNIQUE NOT NULL,
    master_file_id BIGINT REFERENCES file_registry(id) ON DELETE SET NULL,
    total_copies INT DEFAULT 1,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Membership table
CREATE TABLE IF NOT EXISTS content_group_members (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES content_groups(id) ON DELETE CASCADE,
    file_id BIGINT NOT NULL REFERENCES file_registry(id) ON DELETE CASCADE,
    
    is_master BOOLEAN DEFAULT FALSE,
    metadata_score INT DEFAULT 0,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    
    UNIQUE(group_id, file_id)
);

-- Indexes for performance
CREATE INDEX idx_content_groups_hash ON content_groups(content_hash);
CREATE INDEX idx_content_groups_master ON content_groups(master_file_id) WHERE master_file_id IS NOT NULL;

CREATE INDEX idx_content_group_members_group ON content_group_members(group_id);
CREATE INDEX idx_content_group_members_file ON content_group_members(file_id);
CREATE INDEX idx_content_group_members_master ON content_group_members(group_id, is_master) WHERE is_master = TRUE;

-- Trigger to update updated_at
CREATE OR REPLACE FUNCTION update_content_groups_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_content_groups_updated_at
    BEFORE UPDATE ON content_groups
    FOR EACH ROW
    EXECUTE FUNCTION update_content_groups_updated_at();