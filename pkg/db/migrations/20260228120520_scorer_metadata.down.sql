DROP INDEX IF EXISTS idx_file_registry_capture;
ALTER TABLE file_registry DROP CONSTRAINT IF EXISTS valid_capture_role;
ALTER TABLE file_registry DROP COLUMN IF EXISTS capture_role;
ALTER TABLE file_registry DROP COLUMN IF EXISTS capture_stem;
ALTER TABLE content_groups DROP COLUMN IF EXISTS exif_metadata;