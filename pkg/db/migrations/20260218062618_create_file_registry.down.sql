-- Cleanup Trigger and function for file_registry
DROP TRIGGER IF EXISTS update_file_registry_updated_at ON file_registry;
DROP FUNCTION IF EXISTS update_updated_at_column();
-- Cleanup for file_registry
DROP INDEX IF EXISTS idx_file_registry_media_type;
DROP INDEX IF EXISTS idx_file_registry_source_root;
DROP INDEX IF EXISTS idx_file_registry_status;
DROP INDEX IF EXISTS idx_file_registry_session;
DROP INDEX IF EXISTS idx_file_registry_hash;
DROP INDEX IF EXISTS idx_file_registry_path;
DROP TABLE IF EXISTS file_registry;
-- Cleanup for scan_sessions
DROP INDEX IF EXISTS idx_scan_sessions_status;
DROP INDEX IF EXISTS idx_scan_sessions_started;
DROP TABLE IF EXISTS scan_sessions;