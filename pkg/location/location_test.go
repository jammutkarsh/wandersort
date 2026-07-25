// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package location

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
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
	if _, err := setup.Exec(`CREATE TABLE geonames_cities (city TEXT, latitude REAL, longitude REAL, region TEXT DEFAULT '', state TEXT DEFAULT '', country TEXT DEFAULT '', country_code TEXT DEFAULT '')`); err != nil {
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

// TestDisambiguate locks the ladder without a DB: a unique name stays bare, a
// name repeated inside one country takes the state, one that only repeats
// abroad takes the country. The country values here are full names because
// that is what geonames_cities.country stores ("India", not "IN" — the ISO
// code lives in country_code, which only the counting subqueries read).
func TestDisambiguate(t *testing.T) {
	tests := []struct {
		name                              string
		city, state, country              string
		nameCnt, countryCnt, inCountryCnt int
		want                              string
	}{
		{"unique name stays bare", "Shimla", "Himachal Pradesh", "India", 1, 1, 1, "Shimla"},
		{"repeats in this country -> state", "Springfield", "Illinois", "United States", 14, 2, 13, "Springfield, Illinois"},
		{"alone here, repeats abroad -> country", "Springfield", "Queensland", "Australia", 14, 2, 1, "Springfield, Australia"},
		{"two countries, one each -> country", "Hyderabad", "Telangana", "India", 2, 2, 1, "Hyderabad, India"},
		{"same country only -> state", "Wadgaon", "Maharashtra", "India", 2, 1, 2, "Wadgaon, Maharashtra"},
		{"nothing to qualify with", "Nowhere", "", "", 2, 1, 2, "Nowhere"},
	}
	for _, tc := range tests {
		got := disambiguate(tc.city, tc.state, tc.country, tc.nameCnt, tc.countryCnt, tc.inCountryCnt)
		if got != tc.want {
			t.Errorf("%s: disambiguate(%q,%q,%q,%d,%d,%d) = %q, want %q",
				tc.name, tc.city, tc.state, tc.country, tc.nameCnt, tc.countryCnt, tc.inCountryCnt, got, tc.want)
		}
	}
}

// splitQualified has to cope with all three name forms, since an anchor may
// have been saved as any of them.
func TestSplitQualified(t *testing.T) {
	tests := []struct {
		in, city string
		quals    []string
	}{
		{"Hyderabad, Telangana, India", "Hyderabad", []string{"Telangana", "India"}},
		{"Hyderabad, India", "Hyderabad", []string{"India"}},
		{"Springfield, Illinois", "Springfield", []string{"Illinois"}},
		{"Shimla", "Shimla", nil},
		{"  Goa  ", "Goa", nil},
	}
	for _, tc := range tests {
		city, quals := splitQualified(tc.in)
		if city != tc.city || !reflect.DeepEqual(quals, tc.quals) {
			t.Errorf("splitQualified(%q) = (%q,%v), want (%q,%v)", tc.in, city, quals, tc.city, tc.quals)
		}
	}
}

// fullName is what every picker shows: city, state and country spelled out,
// skipping whatever the gazetteer doesn't have.
func TestFullName(t *testing.T) {
	tests := []struct{ city, state, country, want string }{
		{"Indore", "Madhya Pradesh", "India", "Indore, Madhya Pradesh, India"},
		{"Banjar", "Himāchal Pradesh", "India", "Banjar, Himachal Pradesh, India"},
		{"Nowhere", "", "", "Nowhere"},
		{"Nowhere", "", "Atlantis", "Nowhere, Atlantis"},
		{"Singapore", "Singapore", "Singapore", "Singapore"}, // no point repeating it
	}
	for _, tc := range tests {
		if got := fullName(tc.city, tc.state, tc.country); got != tc.want {
			t.Errorf("fullName(%q,%q,%q) = %q, want %q", tc.city, tc.state, tc.country, got, tc.want)
		}
	}
}

func TestMatchesQualifiers(t *testing.T) {
	tests := []struct {
		state, country string
		quals          []string
		want           bool
	}{
		{"Telangana", "India", nil, true}, // a bare saved name still resolves
		{"Telangana", "India", []string{"India"}, true},
		{"Telangana", "India", []string{"Telangana", "India"}, true},
		{"Telangana", "India", []string{"Sindh", "Pakistan"}, false},
		{"Telangana", "India", []string{"Telangana", "Pakistan"}, false},
		{"Himāchal Pradesh", "India", []string{"Himachal Pradesh"}, true}, // diacritics stripped
	}
	for _, tc := range tests {
		if got := matchesQualifiers(tc.state, tc.country, tc.quals); got != tc.want {
			t.Errorf("matchesQualifiers(%q,%q,%v) = %v, want %v", tc.state, tc.country, tc.quals, got, tc.want)
		}
	}
}

// realLocationDB opens the actual downloaded location.db, fetching it first if
// this machine doesn't have it yet — a test that asserts against the shipped
// schema is worthless if it silently skips on a fresh checkout. It downloads to
// the normal ~/.wandersort location (not a temp dir) so the ~116MB fetch happens
// once per machine, not once per test run. Skips only when the download itself
// can't happen (offline CI) or the file is locked by a running wandersort.
func realLocationDB(t *testing.T) *Resolver {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	dbPath := filepath.Join(home, ".wandersort", LocationDBFileName)
	if err := Setup(context.Background(), logger.NewNoopLogger(), dbPath, nil); err != nil {
		t.Skipf("location.db unavailable and download failed: %v", err)
	}

	d, err := db.New(context.Background(), dbPath, db.LocationDB, logger.NewNoopLogger())
	if err != nil {
		// The DB opens locking_mode=EXCLUSIVE, so a scan/serve/config running
		// alongside the tests owns it — not a failure of the code under test.
		t.Skipf("cannot open %s (another wandersort process may be holding it): %v", dbPath, err)
	}
	t.Cleanup(func() { d.Close() })
	return &Resolver{db: d, log: logger.NewNoopLogger()}
}

// TestCandidatesDisambiguatesRealDB runs issue #2 against the actual downloaded
// location.db (the schema the code ships against), not a fabricated fixture.
func TestCandidatesDisambiguatesRealDB(t *testing.T) {
	r := realLocationDB(t)

	// Shimla is unique -> bare; Hyderabad is one per country -> country;
	// Springfield repeats inside the US -> its state.
	cases := []struct {
		name           string
		lat, lon       float64
		want, wantFull string
	}{
		{"Shimla", 31.10442, 77.16662, "Shimla", "Shimla, Himachal Pradesh, India"},
		{"Hyderabad", 17.38405, 78.45636, "Hyderabad, India", "Hyderabad, Telangana, India"},
		{"Springfield", 39.80172, -89.64371, "Springfield, Illinois", "Springfield, Illinois, United States"},
	}
	for _, tc := range cases {
		cands, err := r.Candidates(context.Background(), tc.lat, tc.lon, 0.09, 8)
		if err != nil {
			t.Fatalf("Candidates(%s): %v", tc.name, err)
		}
		var got, gotFull string
		for _, c := range cands {
			if c.Name == tc.name {
				got, gotFull = c.DisplayName, c.FullName
				break
			}
		}
		if got != tc.want {
			t.Errorf("%s DisplayName = %q, want %q (cands=%+v)", tc.name, got, tc.want, cands)
		}
		if gotFull != tc.wantFull {
			t.Errorf("%s FullName = %q, want %q", tc.name, gotFull, tc.wantFull)
		}
	}
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

// TestResolveByNameHonoursQualifier is the round trip that matters: the picker
// offers "Hyderabad, India", the wizard saves exactly that, and resolving it
// has to land on the Indian city — not whichever same-named row the DB returns
// first. Runs against the real gazetteer, since the bug only exists where the
// duplicate does.
func TestResolveByNameHonoursQualifier(t *testing.T) {
	r := realLocationDB(t)

	// The full name is what the town picker offers and what an anchor is saved
	// as, so that form has to resolve; the shorter ones stay supported for
	// anchors saved earlier.
	india, _, err := r.ResolveByName(context.Background(), "Hyderabad, Telangana, India")
	if err != nil {
		t.Fatalf("resolve full name: %v", err)
	}
	if short, _, err := r.ResolveByName(context.Background(), "Hyderabad, India"); err != nil || short != india {
		t.Errorf("short form resolved to %v (err %v), want the same city as the full name (%v)", short, err, india)
	}
	pakistan, _, err := r.ResolveByName(context.Background(), "Hyderabad, Sindh, Pakistan")
	if err != nil {
		t.Fatalf("resolve full name: %v", err)
	}
	if india == pakistan {
		t.Fatalf("both Hyderabads resolved to the same latitude %v — the qualifier was ignored", india)
	}
	// ~17.4°N is Telangana; ~25.4°N is Sindh.
	if india < 16 || india > 19 {
		t.Errorf("Hyderabad, India latitude = %v, want ~17.4", india)
	}
	if pakistan < 24 || pakistan > 27 {
		t.Errorf("Hyderabad, Pakistan latitude = %v, want ~25.4", pakistan)
	}

	// A bare name still resolves (anchors saved before qualifiers existed).
	if _, _, err := r.ResolveByName(context.Background(), "Shimla"); err != nil {
		t.Errorf("unqualified name must still resolve: %v", err)
	}
}

// The picker must be able to find a name it previously handed out, qualifier
// and all — otherwise re-opening the wizard rejects the town it saved itself.
func TestSearchByNameFindsQualifiedName(t *testing.T) {
	r := realLocationDB(t)

	matches, err := r.SearchByName(context.Background(), "Hyderabad, Telangana, India", 8)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("a full name must find its own row again")
	}
	for _, m := range matches {
		if m.FullName != "Hyderabad, Telangana, India" {
			t.Errorf("match %+v, want only Hyderabad, Telangana, India", m)
		}
	}

	// A plain prefix still lists every Hyderabad, each one distinguishable, and
	// "Banjar" — which the gazetteer holds twice for the same state — is listed
	// once: two identical rows are not a choice.
	for _, prefix := range []string{"Hyderabad", "Banjar"} {
		all, err := r.SearchByName(context.Background(), prefix, 8)
		if err != nil {
			t.Fatalf("SearchByName(%s): %v", prefix, err)
		}
		seen := map[string]bool{}
		for _, m := range all {
			if seen[m.FullName] {
				t.Errorf("%s: two rows share the full name %q — the picker can't tell them apart", prefix, m.FullName)
			}
			seen[m.FullName] = true
		}
		if len(seen) < 2 {
			t.Errorf("%s: expected several matches, got %v", prefix, seen)
		}
	}
}
