package locationdb

import (
	"context"
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

func TestLookup_ExpectedCity(t *testing.T) {
	db := makeFixtureDB(t)
	ldb := &DB{db: db, cache: make(map[cacheKey]string)}

	city, err := ldb.Lookup(context.Background(), 16.88, 96.159)
	if err != nil {
		t.Fatalf("Lookup error: %v", err)
	}
	if city != "Yangon" {
		t.Errorf("got %q, want %q", city, "Yangon")
	}
}

func TestLookup_FarFromAnyPlace(t *testing.T) {
	db := makeFixtureDB(t)
	ldb := &DB{db: db, cache: make(map[cacheKey]string)}

	_, err := ldb.Lookup(context.Background(), 0.0, 0.0)
	if err != ErrNoLocation {
		t.Errorf("got %v, want ErrNoLocation", err)
	}
}

func TestLookup_CacheHit(t *testing.T) {
	db := makeFixtureDB(t)
	ldb := &DB{db: db, cache: make(map[cacheKey]string)}

	ctx := context.Background()
	city1, err := ldb.Lookup(ctx, 16.88, 96.159)
	if err != nil {
		t.Fatalf("first Lookup error: %v", err)
	}

	// Close the underlying DB to prove the second call serves from cache.
	db.Close()

	city2, err := ldb.Lookup(ctx, 16.88, 96.159)
	if err != nil {
		t.Fatalf("second Lookup (cache) error: %v", err)
	}
	if city1 != city2 {
		t.Errorf("cache returned %q, want %q", city2, city1)
	}
}
