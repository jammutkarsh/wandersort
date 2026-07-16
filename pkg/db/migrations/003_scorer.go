package migrations

var schema003 = Migration{
	Version:     0o03,
	Description: "scorer_is_master",
	SQL: []string{
		isMaster,
	},
}

// Every file is a master by default; the scorer demotes the losers of each
// duplicate group. Solo files are never touched and stay masters.
const isMaster = `
ALTER TABLE file_metadata ADD COLUMN is_master INTEGER NOT NULL DEFAULT 1;
`
