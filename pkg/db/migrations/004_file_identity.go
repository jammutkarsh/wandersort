package migrations

// schema004 makes a file its volume plus its path, and adds its modification
// time as an instant. Columns are altered in place: dropping file_registry to
// rebuild it would cascade into every table that references it.
var schema004 = Migration{
	Version:     4,
	Description: "file_identity",
	// a library made between releases was created with these columns
	Unless: `SELECT EXISTS (SELECT 1 FROM pragma_table_info('file_registry') WHERE name = 'file_mtime_ns')`,
	SQL: []string{
		`DROP INDEX idx_file_registry_dir_name`,

		// The same time as file_modified_at, as Unix nanoseconds, for change
		// detection: the wall-clock column moves with the computer's time zone
		// on APFS/NTFS, this one on FAT/exFAT (which store wall clock), never
		// both at once. 0 until the file is recorded afresh
		`ALTER TABLE file_registry ADD COLUMN file_mtime_ns INTEGER NOT NULL DEFAULT 0`,

		// Volume the file lives on; '' when it can't be read, and for a placed
		// file: a library path names no source volume
		`ALTER TABLE file_registry RENAME COLUMN volume_uuid TO nullable_volume_uuid`,
		`ALTER TABLE file_registry ADD COLUMN volume_uuid TEXT NOT NULL DEFAULT ''`,
		`UPDATE file_registry SET volume_uuid = nullable_volume_uuid
			WHERE placed = 0 AND nullable_volume_uuid IS NOT NULL`,
		`ALTER TABLE file_registry DROP COLUMN nullable_volume_uuid`,

		// Physical identity: volume + absolute directory + name. Two cards
		// mounted in turn at one path hold different files
		`CREATE UNIQUE INDEX idx_file_registry_identity ON file_registry(file_dir, file_name, volume_uuid)`,
	},
}
