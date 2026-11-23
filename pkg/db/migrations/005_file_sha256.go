package migrations

// schema005 adds the second hash: file_hash groups files, file_sha256 confirms
// a match and is what copies are verified against.
var schema005 = Migration{
	Version:     5,
	Description: "file_sha256",
	SQL: []string{
		`ALTER TABLE file_metadata ADD COLUMN file_sha256 TEXT NOT NULL DEFAULT ''`,
	},
}
