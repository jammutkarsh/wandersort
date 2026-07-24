// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package location

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	_ "modernc.org/sqlite"
)

func TestStripDiacritics(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Banjār", "Banjar"},
		{"São Paulo", "Sao Paulo"},
		{"Delhi", "Delhi"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := stripDiacritics(tc.in); got != tc.want {
			t.Errorf("stripDiacritics(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// newTestLocationDB builds a minimal geonames_cities fixture — the real
// location.db is a downloaded asset, so tests fabricate just the schema and
// rows a query needs, written then reopened read-only like openLocationDB does.
func newTestLocationDB(t *testing.T, rows [][3]any) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "location.db")

	setup, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(`CREATE TABLE geonames_cities (city TEXT, latitude REAL, longitude REAL)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := setup.Exec(`INSERT INTO geonames_cities (city, latitude, longitude) VALUES (?, ?, ?)`, r[0], r[1], r[2]); err != nil {
			t.Fatal(err)
		}
	}
	if err := setup.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := db.New(context.Background(), path, db.LocationDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestQueryNearestAcceptsWiderRadius(t *testing.T) {
	// ~30km away — inside the raised 50km threshold, was silently dropped
	// before MaxDistSquared matched the wider bounding box.
	d := newTestLocationDB(t, [][3]any{{"Shimla", 31.1048, 77.1734}})
	r := &Resolver{db: d, log: logger.NewNoopLogger()}

	// roughly 30km north of Shimla
	city, err := r.queryNearest(context.Background(), 31.37, 77.1734)
	if err != nil {
		t.Fatalf("queryNearest: %v", err)
	}
	if city != "Shimla" {
		t.Errorf("city = %q, want Shimla", city)
	}
}

func TestQueryNearestStripsDiacritics(t *testing.T) {
	d := newTestLocationDB(t, [][3]any{{"Banjār", 31.6340, 77.3706}})
	r := &Resolver{db: d, log: logger.NewNoopLogger()}

	city, err := r.queryNearest(context.Background(), 31.6340, 77.3706)
	if err != nil {
		t.Fatalf("queryNearest: %v", err)
	}
	if city != "Banjar" {
		t.Errorf("city = %q, want diacritic-stripped Banjar", city)
	}
}

// TestCandidatesPrefersPlainSpelling covers the real case: two gazetteer
// entries for the same place, one plain and one carrying diacritics, with the
// diacritic one a hair closer — the plain entry should still win.
func TestCandidatesPrefersPlainSpelling(t *testing.T) {
	d := newTestLocationDB(t, [][3]any{
		{"Banjār", 31.6340, 77.3706}, // slightly closer to the query point
		{"Banjar", 31.6341, 77.3707},
	})
	r := &Resolver{db: d, log: logger.NewNoopLogger()}

	cands, err := r.Candidates(context.Background(), 31.6340, 77.3706, 0.45, 8)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("cands = %+v, want 2", cands)
	}
	if cands[0].Name != "Banjar" {
		t.Errorf("cands[0].Name = %q, want plain-spelled Banjar to rank first despite being marginally farther", cands[0].Name)
	}
}

func TestCandidatesRanksByDistance(t *testing.T) {
	d := newTestLocationDB(t, [][3]any{
		{"Near", 31.0, 77.0},
		{"Far", 31.05, 77.0},
	})
	r := &Resolver{db: d, log: logger.NewNoopLogger()}

	cands, err := r.Candidates(context.Background(), 31.0, 77.0, 0.45, 8)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(cands) != 2 || cands[0].Name != "Near" || cands[1].Name != "Far" {
		t.Fatalf("cands = %+v, want [Near, Far]", cands)
	}
	if cands[0].DistKM >= cands[1].DistKM {
		t.Errorf("expected Near closer than Far: %+v", cands)
	}
}

func TestResolveByName(t *testing.T) {
	d := newTestLocationDB(t, [][3]any{{"Manali", 32.2432, 77.1892}})
	r := &Resolver{db: d, log: logger.NewNoopLogger()}

	lat, lon, err := r.ResolveByName(context.Background(), "manali")
	if err != nil {
		t.Fatalf("ResolveByName: %v", err)
	}
	if lat != 32.2432 || lon != 77.1892 {
		t.Errorf("got (%v, %v), want (32.2432, 77.1892)", lat, lon)
	}

	if _, _, err := r.ResolveByName(context.Background(), "Nowhereville"); err != ErrNoLocation {
		t.Errorf("err = %v, want ErrNoLocation", err)
	}
}

func TestSearchByName(t *testing.T) {
	d := newTestLocationDB(t, [][3]any{
		{"Delhi", 28.6139, 77.2090},
		{"Delray Beach", 26.4615, -80.0728},
		{"Manali", 32.2432, 77.1892},
	})
	r := &Resolver{db: d, log: logger.NewNoopLogger()}

	matches, err := r.SearchByName(context.Background(), "del", 8)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %+v, want 2 (Delhi, Delray Beach)", matches)
	}
	names := map[string]bool{}
	for _, m := range matches {
		names[m.Name] = true
	}
	if !names["Delhi"] || !names["Delray Beach"] {
		t.Errorf("matches = %+v, want Delhi and Delray Beach", matches)
	}
}

// TestResolveByNameMatchesStrippedName covers the anchor round-trip: every name
// this package hands out is diacritic-stripped (SearchByName, Candidates), so a
// saved anchor "Banjar" must still resolve to the gazetteer's "Banjār". With
// exact-match-only, anchors for such places silently never resolved and the
// whole home/work folding feature was a no-op for those users.
func TestResolveByNameMatchesStrippedName(t *testing.T) {
	d := newTestLocationDB(t, [][3]any{{"Banjār", 31.6383, 77.3403}})
	r := &Resolver{db: d, log: logger.NewNoopLogger()}

	matches, err := r.SearchByName(context.Background(), "Banj", 8)
	if err != nil || len(matches) != 1 || matches[0].Name != "Banjar" {
		t.Fatalf("SearchByName = %+v, %v; want the stripped name the user would save", matches, err)
	}

	lat, lon, err := r.ResolveByName(context.Background(), matches[0].Name)
	if err != nil {
		t.Fatalf("ResolveByName(%q): %v", matches[0].Name, err)
	}
	if lat != 31.6383 || lon != 77.3403 {
		t.Errorf("got (%v, %v), want Banjār's coordinates", lat, lon)
	}
}
