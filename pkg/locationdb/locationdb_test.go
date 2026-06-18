package locationdb

import (
	"database/sql"
	"os"
	"testing"
)

// makeFixtureDB creates a minimal temp-file SQLite DB with a locations table
// matching the schema expected by queryNearest.
func makeFixtureDB(t *testing.T) *sql.DB {
	t.Helper()

	f, err := os.CreateTemp("", "locationdb-test-*.db")
	if err != nil {
		t.Fatalf("create temp db file: %v", err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	db, err := sql.Open("sqlite", f.Name())
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}

	_, err = db.Exec(`CREATE TABLE geonames_cities (city TEXT, latitude REAL, longitude REAL)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, err = db.Exec(`INSERT INTO geonames_cities VALUES (?, ?, ?)`, "Yangon", 16.8409, 96.1735)
	if err != nil {
		t.Fatalf("insert fixture row: %v", err)
	}

	return db
}
