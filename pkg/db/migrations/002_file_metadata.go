package migrations

var schema002 = Migration{
	Version:     0o02,
	Description: "file_metadata_schema",
	SQL: []string{
		fileMetadata,
	},
}

const fileMetadata = `
CREATE TABLE IF NOT EXISTS file_metadata (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_hash TEXT NOT NULL,
    file_id INTEGER REFERENCES file_registry(id) ON DELETE SET NULL,

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

CREATE UNIQUE INDEX IF NOT EXISTS idx_file_metadata_hash_file ON file_metadata(file_hash, file_id);

CREATE TRIGGER IF NOT EXISTS trg_file_metadata_hashed
AFTER INSERT ON file_metadata
FOR EACH ROW
BEGIN
    UPDATE file_registry SET scan_status = 'HASHED' WHERE id = NEW.file_id;
END;
`
