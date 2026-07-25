// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// These tests run against the real, downloaded location.db (via
// locationtest.Resolver) rather than a fabricated fixture — they assert
// against actual gazetteer rows, discovered once by inspecting the shipped
// database (see each test's comment for the row(s) it depends on). External
// package (location_test) since locationtest imports location, and only
// exported API is needed here.
package location_test

import (
	"context"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/location/locationtest"
)

// TestCandidatesWidensSearchRadius exercises the two-pass expanding box
// (queryNearest's ~10km then ~50km bounding boxes) against a point near
// Shimla, Himachal Pradesh (31.10442, 77.16662) chosen because every other
// Himachal town in range sits far enough away that the tight ~10km box
// (delta 0.09) is provably empty from this point, while the ~50km box
// (delta 0.45) finds a match — this is the exact bug MaxDistSquared and the
// box delta disagreeing used to cause: a match 15-40km out silently dropped.
func TestCandidatesWidensSearchRadius(t *testing.T) {
	r := locationtest.Resolver(t)
	ctx := context.Background()

	// ~30km north of Shimla.
	lat, lon := 31.10442+0.27, 77.16662

	tight, err := r.Candidates(ctx, lat, lon, 0.09, 5)
	if err != nil {
		t.Fatalf("Candidates(tight): %v", err)
	}
	if len(tight) != 0 {
		t.Fatalf("tight (~10km) box = %+v, want empty — point is >10km from every real city here", tight)
	}

	wide, err := r.Candidates(ctx, lat, lon, 0.45, 5)
	if err != nil {
		t.Fatalf("Candidates(wide): %v", err)
	}
	if len(wide) == 0 {
		t.Fatal("wide (~50km) box found nothing — a real match in this range was silently dropped")
	}
	if wide[0].DistKM <= 10 || wide[0].DistKM > 50 {
		t.Errorf("nearest match = %.1fkm, want strictly between the tight and wide radii (10km, 50km]", wide[0].DistKM)
	}
}

// TestCandidatesPrefersPlainSpelling covers the real case the gazetteer
// actually has: Panauti, Bagmati Province, Nepal exists as both a plain
// entry (27.58466, 85.52122) and a diacritic one, "Panauti̇̄" (27.58447,
// 85.51487), a few hundred metres apart. Querying exactly at the diacritic
// entry's coordinates makes it the nearer of the two by raw distance. The
// box also picks up other real Bagmati Province towns (Dhulikhel, Banepa) in
// between the two Panauti rows in rank order — this only checks the two
// Panauti entries' order relative to each other, not their absolute
// position, since real data isn't as clean as a two-row fixture.
func TestCandidatesPrefersPlainSpelling(t *testing.T) {
	r := locationtest.Resolver(t)
	ctx := context.Background()

	cands, err := r.Candidates(ctx, 27.58447, 85.51487, 0.09, 5)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	plainIdx, diacriticIdx := -1, -1
	for i, c := range cands {
		if c.Name != "Panauti" {
			continue
		}
		if c.DistKM < 0.01 {
			diacriticIdx = i // the diacritic row sits exactly at the query point
		} else {
			plainIdx = i
		}
	}
	if plainIdx == -1 || diacriticIdx == -1 {
		t.Fatalf("cands = %+v, want both the plain and diacritic Panauti entries", cands)
	}
	if plainIdx >= diacriticIdx {
		t.Errorf("cands = %+v, want the plain entry (farther) ranked ahead of the diacritic one (nearer, at the query point)", cands)
	}
}

// TestCandidatesRanksByDistance queries exactly at Shimla's own coordinates,
// so it is the nearest possible match (~0km), and checks the rest of the box
// comes back in non-decreasing distance order.
func TestCandidatesRanksByDistance(t *testing.T) {
	r := locationtest.Resolver(t)
	ctx := context.Background()

	cands, err := r.Candidates(ctx, 31.10442, 77.16662, 0.45, 5)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(cands) < 2 {
		t.Fatalf("cands = %+v, want at least 2 (Shimla plus a neighbour)", cands)
	}
	if cands[0].Name != "Shimla" || cands[0].DistKM > 0.01 {
		t.Errorf("cands[0] = %+v, want Shimla at ~0km (query point is its own coordinates)", cands[0])
	}
	for i := 1; i < len(cands); i++ {
		if cands[i].DistKM < cands[i-1].DistKM {
			t.Errorf("cands not sorted by distance: %+v", cands)
		}
	}
}

// TestResolveByName covers Manali, Tamil Nadu (13.16667, 80.26667) — a name
// most readers associate with the Himachal hill town, but the gazetteer's
// only exact "Manali" row is this one, which is exactly why ResolveByName
// has to be driven by what's actually in the DB, not assumption.
func TestResolveByName(t *testing.T) {
	r := locationtest.Resolver(t)
	ctx := context.Background()

	lat, lon, err := r.ResolveByName(ctx, "manali")
	if err != nil {
		t.Fatalf("ResolveByName: %v", err)
	}
	if lat != 13.16667 || lon != 80.26667 {
		t.Errorf("got (%v, %v), want (13.16667, 80.26667)", lat, lon)
	}

	if _, _, err := r.ResolveByName(ctx, "DefinitelyNotARealPlace9182XyZ"); err != location.ErrNoLocation {
		t.Errorf("err = %v, want ErrNoLocation", err)
	}
}

// TestResolveByNameMatchesStrippedName covers the anchor round-trip: every
// name this package hands out is diacritic-stripped (SearchByName,
// Candidates), so a saved anchor "Banjar, Himachal Pradesh, India" must
// still resolve to the gazetteer's diacritic "Banjār" row (31.639, 77.34055)
// — the only "Banjar"-named place in Himachal Pradesh.
func TestResolveByNameMatchesStrippedName(t *testing.T) {
	r := locationtest.Resolver(t)
	ctx := context.Background()

	matches, err := r.SearchByName(ctx, "Banjār", 5)
	if err != nil || len(matches) != 1 || matches[0].Name != "Banjar" {
		t.Fatalf("SearchByName = %+v, %v; want the single Himachal Pradesh row, stripped", matches, err)
	}
	if matches[0].FullName != "Banjar, Himachal Pradesh, India" {
		t.Errorf("FullName = %q, want %q", matches[0].FullName, "Banjar, Himachal Pradesh, India")
	}

	lat, lon, err := r.ResolveByName(ctx, matches[0].FullName)
	if err != nil {
		t.Fatalf("ResolveByName(%q): %v", matches[0].FullName, err)
	}
	if lat != 31.639 || lon != 77.34055 {
		t.Errorf("got (%v, %v), want Banjār's coordinates (31.639, 77.34055)", lat, lon)
	}
}

// TestSearchByName covers the real ambiguous case: the gazetteer holds five
// distinct "Delhi" rows (India, and four in the US/Canada) — every one must
// come back with its own distinct FullName, none dropped as a false
// duplicate — plus an unrelated, unambiguous prefix match. The "Delhi"
// prefix also matches "Delhi Cantonment" and "Delhi Hills"; those are
// filtered out below since this test is only about the exact-name rows.
func TestSearchByName(t *testing.T) {
	r := locationtest.Resolver(t)
	ctx := context.Background()

	all, err := r.SearchByName(ctx, "Delhi", 10)
	if err != nil {
		t.Fatalf("SearchByName(Delhi): %v", err)
	}
	wantFullNames := map[string]bool{
		"Delhi, India":                     false,
		"Delhi, Louisiana, United States":  false,
		"Delhi, New York, United States":   false,
		"Delhi, California, United States": false,
		"Delhi, Ontario, Canada":           false,
	}
	var delhis int
	for _, m := range all {
		if m.Name != "Delhi" {
			continue
		}
		delhis++
		if _, ok := wantFullNames[m.FullName]; !ok {
			t.Errorf("unexpected FullName %q", m.FullName)
		}
		wantFullNames[m.FullName] = true
	}
	if delhis != len(wantFullNames) {
		t.Fatalf("matches = %+v, want %d distinct Delhi rows", all, len(wantFullNames))
	}
	for full, seen := range wantFullNames {
		if !seen {
			t.Errorf("missing expected match %q", full)
		}
	}

	delray, err := r.SearchByName(ctx, "Delray", 5)
	if err != nil {
		t.Fatalf("SearchByName(Delray): %v", err)
	}
	if len(delray) != 1 || delray[0].Name != "Delray Beach" || delray[0].FullName != "Delray Beach, Florida, United States" {
		t.Fatalf("matches = %+v, want exactly Delray Beach, Florida, United States", delray)
	}
}

// TestCandidatesDisambiguatesRealDB runs issue #2 against the actual
// downloaded location.db (the schema the code ships against), not a
// fabricated fixture.
func TestCandidatesDisambiguatesRealDB(t *testing.T) {
	r := locationtest.Resolver(t)
	ctx := context.Background()

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
		cands, err := r.Candidates(ctx, tc.lat, tc.lon, 0.09, 8)
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

// TestResolveByNameHonoursQualifier is the round trip that matters: the
// picker offers "Hyderabad, India", the wizard saves exactly that, and
// resolving it has to land on the Indian city — not whichever same-named row
// the DB returns first. Runs against the real gazetteer, since the bug only
// exists where the duplicate does.
func TestResolveByNameHonoursQualifier(t *testing.T) {
	r := locationtest.Resolver(t)
	ctx := context.Background()

	// The full name is what the town picker offers and what an anchor is
	// saved as, so that form has to resolve; the shorter ones stay supported
	// for anchors saved earlier.
	india, _, err := r.ResolveByName(ctx, "Hyderabad, Telangana, India")
	if err != nil {
		t.Fatalf("resolve full name: %v", err)
	}
	if short, _, err := r.ResolveByName(ctx, "Hyderabad, India"); err != nil || short != india {
		t.Errorf("short form resolved to %v (err %v), want the same city as the full name (%v)", short, err, india)
	}
	pakistan, _, err := r.ResolveByName(ctx, "Hyderabad, Sindh, Pakistan")
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
	if _, _, err := r.ResolveByName(ctx, "Shimla"); err != nil {
		t.Errorf("unqualified name must still resolve: %v", err)
	}
}

// The picker must be able to find a name it previously handed out, qualifier
// and all — otherwise re-opening the wizard rejects the town it saved itself.
func TestSearchByNameFindsQualifiedName(t *testing.T) {
	r := locationtest.Resolver(t)
	ctx := context.Background()

	matches, err := r.SearchByName(ctx, "Hyderabad, Telangana, India", 8)
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

	// A plain prefix still lists every Hyderabad, each one distinguishable,
	// and "Banjar" — which the gazetteer holds twice for the same state — is
	// listed once: two identical rows are not a choice.
	for _, prefix := range []string{"Hyderabad", "Banjar"} {
		all, err := r.SearchByName(ctx, prefix, 8)
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
