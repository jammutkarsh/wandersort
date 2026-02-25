DROP TRIGGER IF EXISTS update_content_groups_updated_at ON content_groups;
DROP FUNCTION IF EXISTS update_content_groups_updated_at();
DROP TABLE IF EXISTS content_group_members CASCADE;
DROP TABLE IF EXISTS content_groups CASCADE;