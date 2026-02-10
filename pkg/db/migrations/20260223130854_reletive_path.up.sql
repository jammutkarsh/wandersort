-- Add path_type column to distinguish relative vs absolute paths
ALTER TABLE file_registry ADD COLUMN path_type VARCHAR(10) NOT NULL DEFAULT 'RELATIVE';

ALTER TABLE file_registry ADD CONSTRAINT valid_path_type
    CHECK (path_type IN ('RELATIVE', 'ABSOLUTE'));

-- Mark all existing paths as absolute (migration safety)
UPDATE file_registry SET path_type = 'ABSOLUTE';

-- Drop old single-column unique index (relative path alone is not unique across roots)
DROP INDEX IF EXISTS idx_file_registry_path;

-- Create composite unique index: relative path + source_root uniquely identifies a file
CREATE UNIQUE INDEX idx_file_registry_path_root ON file_registry(file_path, source_root);

COMMENT ON COLUMN file_registry.file_path IS 'Path relative to source_root (e.g., "2023/vacation/img.jpg")';
COMMENT ON COLUMN file_registry.source_root IS 'Base path, may use ~ notation (e.g., "~/Photos")';

-- Track whether a file is from an original source or an organized output directory
ALTER TABLE file_registry ADD COLUMN file_origin VARCHAR(20) NOT NULL DEFAULT 'SOURCE';

ALTER TABLE file_registry ADD CONSTRAINT valid_file_origin
    CHECK (file_origin IN ('SOURCE', 'ORGANIZED', 'UNKNOWN'));

-- Index for querying/filtering by origin
CREATE INDEX idx_file_registry_origin ON file_registry(file_origin);

COMMENT ON COLUMN file_registry.file_origin IS 'SOURCE = original files, ORGANIZED = files in output directory after organization';
