-- Restore original single-column unique index
DROP INDEX IF EXISTS idx_file_registry_path_root;
CREATE UNIQUE INDEX idx_file_registry_path ON file_registry(file_path);

ALTER TABLE file_registry DROP CONSTRAINT IF EXISTS valid_path_type;
ALTER TABLE file_registry DROP COLUMN IF EXISTS path_type;

DROP INDEX IF EXISTS idx_file_registry_origin;
ALTER TABLE file_registry DROP CONSTRAINT IF EXISTS valid_file_origin;
ALTER TABLE file_registry DROP COLUMN IF EXISTS file_origin;
