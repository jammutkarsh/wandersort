package locationdb_test

import (
	"context"
	"database/sql"
	"math"
	"os"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/locationdb"
)

func TestParseGPS_ValidDMS(t *testing.T) {
	lat, lon, err := locationdb.ParseGPS(`31 deg 34' 5.84" N`, `77 deg 22' 14.32" E`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantLat := 31 + 34.0/60 + 5.84/3600
	wantLon := 77 + 22.0/60 + 14.32/3600

	if !almostEqual(lat, wantLat, 1e-6) {
		t.Errorf("lat = %v, want %v", lat, wantLat)
	}
	if !almostEqual(lon, wantLon, 1e-6) {
		t.Errorf("lon = %v, want %v", lon, wantLon)
	}
}

func TestParseGPS_SouthWestNegative(t *testing.T) {
	lat, lon, err := locationdb.ParseGPS(`33 deg 52' 0.00" S`, `151 deg 12' 36.00" W`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if lat >= 0 {
		t.Errorf("expected negative latitude for S, got %v", lat)
	}

	if lon >= 0 {
		t.Errorf("expected negative longitude for W, got %v", lon)
	}
}

func TestParseGPS_InvalidInput_NoHemishpere(t *testing.T) {
	_, _, err := locationdb.ParseGPS(`31 deg 34' 5.84"`, `77 deg 22' 14.32" E`)
	if err == nil {
		t.Error("expected error for missing hemisphere, got nil")
	}
}

func TestParseGPS_InvalidInput_Empty(t *testing.T) {
	_, _, err := locationdb.ParseGPS("", "")
	if err == nil {
		t.Error("expected error for empty string, got nil")
	}
}

func TestParseGPS_InvalidInput_GarbageText(t *testing.T) {
	_, _, err := locationdb.ParseGPS("not a coordinate", "also garbage")
	if err == nil {
		t.Error("expected error for garbage input, got nil")
	}
}

func TestLookup_ExpectedCity(t *testing.T) {
	dbPath := fixtureDB(t)

	db, err := locationdb.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	city, err := db.Lookup(context.Background(), 16.74, 96.07)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	if city != "Yangon" {
		t.Fatalf("expected %q, got %q", "Yangon", city)
	}
}

func TestLookup_FarFromAnyPlace(t *testing.T) {
	dbPath := fixtureDB(t)

	db, err := locationdb.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	_, err = db.Lookup(context.Background(), 0.0, -16.80)
	if err == nil {
		t.Fatal("expected ErrNoLocation, got nil")
	}
}

func TestLookup_CacheHit(t *testing.T) {
	dbPath := fixtureDB(t)

	db, err := locationdb.Open(dbPath)
	if err != nil {
		t.Fatalf("opne: %v", err)
	}

	ctx := context.Background()

	city1, err := db.Lookup((ctx), 16.74, 96.07)
	if err != nil {
		t.Fatalf("first lookup: %v", err)
	}

	city2, err := db.Lookup(ctx, 16.74, 96.07)
	if err != nil {
		t.Fatalf("second lookup: %v", err)
	}

	if city1 != city2 {
		t.Fatalf("cache inconsistency: %q vs %q", city1, city2)
	}
}

func fixtureDB(t *testing.T) string {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "locationdb-*.sqlite")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	f.Close()

	db, err := sql.Open("sqlite", f.Name())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE locations (
			name      TEXT    NOT NULL,
			latitude  REAL    NOT NULL,
			longitude REAL    NOT NULL
		);
		CREATE INDEX idx_lat ON locations(latitude);
		CREATE INDEX idx_lon ON locations(longitude);

		INSERT INTO locations VALUES ('Yangon',      16.8409, 96.1735);
		INSERT INTO locations VALUES ('Mandalay',    21.9588, 96.0891);
		INSERT INTO locations VALUES ('Bago',        17.3364, 96.4797);
	`)

	if err != nil {
		t.Fatalf("seed fixture DB: %v", err)
	}

	return f.Name()
}

func almostEqual(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}
