// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package integration holds tests that run against the real, downloaded
// location.db rather than fabricated fixtures — they assert against actual
// gazetteer rows, discovered once by inspecting the shipped database (see
// each test's comment for the row(s) it depends on).
package integration

import (
	"context"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/location/locationtest"
)

// TestCandidates exercises Candidates against real gazetteer rows: expanding
// search radius, plain-spelling preference over diacritics, and distance sort.
func TestCandidates(t *testing.T) {
	r := locationtest.Resolver(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		lat, lon float64
		radius   float64
		limit    int
		check    func(t *testing.T, cands []location.Candidate)
	}{
		{
			name: "widens search radius from tight to wide box",
			// Point ~30km north of Shimla, deliberately placed where the ~10km box
			// is empty but ~50km finds a match — the bug MaxDistSquared and the box
			// delta disagreeing used to silently drop matches 15-40km out.
			lat: 31.10442 + 0.27, lon: 77.16662, radius: 0.09, limit: 5,
			check: func(t *testing.T, cands []location.Candidate) {
				if len(cands) != 0 {
					t.Fatalf("tight (~10km) box = %+v, want empty", cands)
				}
			},
		},
		{
			name: "widens search radius from tight to wide box",
			lat:  31.10442 + 0.27, lon: 77.16662, radius: 0.45, limit: 5,
			check: func(t *testing.T, cands []location.Candidate) {
				if len(cands) == 0 {
					t.Fatal("wide (~50km) box found nothing")
				}
				if cands[0].DistKM <= 10 || cands[0].DistKM > 50 {
					t.Errorf("nearest = %.1fkm, want (10km, 50km]", cands[0].DistKM)
				}
			},
		},
		{
			name: "prefers plain spelling over diacritic at the same location",
			// Panauti, Bagmati Province, Nepal exists as plain "Panauti"
			// (27.58466, 85.52122) and diacritic "Panauti̇̄" (27.58447, 85.51487).
			// Querying at the diacritic entry makes it nearer by raw distance;
			// the plain entry must still rank ahead.
			lat: 27.58447, lon: 85.51487, radius: 0.09, limit: 5,
			check: func(t *testing.T, cands []location.Candidate) {
				plainIdx, diacriticIdx := -1, -1
				for i, c := range cands {
					if c.Name != "Panauti" {
						continue
					}
					if c.DistKM < 0.01 {
						diacriticIdx = i
					} else {
						plainIdx = i
					}
				}
				if plainIdx == -1 || diacriticIdx == -1 {
					t.Fatalf("cands = %+v, want both plain and diacritic Panauti", cands)
				}
				if plainIdx >= diacriticIdx {
					t.Errorf("cands = %+v, want plain entry ranked ahead of diacritic", cands)
				}
			},
		},
		{
			name: "ranks results by non-decreasing distance",
			lat:  31.10442, lon: 77.16662, radius: 0.45, limit: 5,
			check: func(t *testing.T, cands []location.Candidate) {
				if len(cands) < 2 {
					t.Fatalf("cands = %+v, want at least 2 (Shimla plus a neighbour)", cands)
				}
				if cands[0].Name != "Shimla" || cands[0].DistKM > 0.01 {
					t.Errorf("cands[0] = %+v, want Shimla at ~0km", cands[0])
				}
				for i := 1; i < len(cands); i++ {
					if cands[i].DistKM < cands[i-1].DistKM {
						t.Errorf("cands not sorted by distance: %+v", cands)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cands, err := r.Candidates(ctx, tt.lat, tt.lon, tt.radius, tt.limit)
			if err != nil {
				t.Fatalf("Candidates: %v", err)
			}
			tt.check(t, cands)
		})
	}
}

// TestResolveByName exercises ResolveByName against real gazetteer rows:
// exact match, fuzzy match, diacritic-stripped round-trip, and qualifier
// disambiguation (Hyderabad, India vs Hyderabad, Pakistan).
func TestResolveByName(t *testing.T) {
	r := locationtest.Resolver(t)
	ctx := context.Background()

	tests := []struct {
		name             string
		input            string
		wantLat, wantLon float64
		wantErr          error
	}{
		{
			name: "exact match (Manali, Tamil Nadu)",
			// The gazetteer's only exact "Manali" row is in Tamil Nadu
			// (13.16667, 80.26667), not the Himachal hill town.
			input:   "manali",
			wantLat: 13.16667, wantLon: 80.26667,
		},
		{
			name:    "nonexistent place",
			input:   "DefinitelyNotARealPlace9182XyZ",
			wantErr: location.ErrNoLocation,
		},
		{
			name: "diacritic-stripped round-trip (Banjār → Banjar)",
			// A saved anchor "Banjar, Himachal Pradesh, India" must resolve to
			// the gazetteer's diacritic "Banjār" row (31.639, 77.34055).
			input:   "Banjar, Himachal Pradesh, India",
			wantLat: 31.639, wantLon: 77.34055,
		},
		{
			name:    "qualifier disambiguates (Hyderabad, Telangana, India)",
			input:   "Hyderabad, Telangana, India",
			wantLat: 17.38405, wantLon: 78.45636,
		},
		{
			name:    "qualifier disambiguates (Hyderabad, Sindh, Pakistan)",
			input:   "Hyderabad, Sindh, Pakistan",
			wantLat: 25.39689, wantLon: 68.37718,
		},
		{
			name: "short qualifier (Hyderabad, India)",
			// The short form must resolve to the same city as the full name.
			input:   "Hyderabad, India",
			wantLat: 17.38405, wantLon: 78.45636,
		},
		{
			name:    "unqualified name still resolves (backwards compat)",
			input:   "Shimla",
			wantLat: 31.10442, wantLon: 77.16662,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lat, lon, err := r.ResolveByName(ctx, tt.input)
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveByName(%q): %v", tt.input, err)
			}
			if lat != tt.wantLat || lon != tt.wantLon {
				t.Errorf("got (%v, %v), want (%v, %v)", lat, lon, tt.wantLat, tt.wantLon)
			}
		})
	}

	// Cross-check: the two Hyderabads must resolve to different coordinates.
	t.Run("India and Pakistan Hyderabad are distinct", func(t *testing.T) {
		india, _, _ := r.ResolveByName(ctx, "Hyderabad, Telangana, India")
		pakistan, _, _ := r.ResolveByName(ctx, "Hyderabad, Sindh, Pakistan")
		if india == pakistan {
			t.Fatalf("both Hyderabads resolved to %v — qualifier ignored", india)
		}
		// ~17.4°N is Telangana; ~25.4°N is Sindh.
		if india < 16 || india > 19 {
			t.Errorf("Hyderabad, India lat = %v, want ~17.4", india)
		}
		if pakistan < 24 || pakistan > 27 {
			t.Errorf("Hyderabad, Pakistan lat = %v, want ~25.4", pakistan)
		}
	})
}

// TestSearchByName exercises SearchByName against real gazetteer rows:
// ambiguous names with distinct FullNames, prefix matches, and the picker
// round-trip (finding a name it previously handed out).
func TestSearchByName(t *testing.T) {
	r := locationtest.Resolver(t)
	ctx := context.Background()

	t.Run("five distinct Delhi rows each with unique FullName", func(t *testing.T) {
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
			t.Fatalf("got %d Delhi rows, want %d: %+v", delhis, len(wantFullNames), all)
		}
		for full, seen := range wantFullNames {
			if !seen {
				t.Errorf("missing expected match %q", full)
			}
		}
	})

	t.Run("prefix match (Delray → Delray Beach)", func(t *testing.T) {
		delray, err := r.SearchByName(ctx, "Delray", 5)
		if err != nil {
			t.Fatalf("SearchByName(Delray): %v", err)
		}
		if len(delray) != 1 || delray[0].Name != "Delray Beach" ||
			delray[0].FullName != "Delray Beach, Florida, United States" {
			t.Fatalf("matches = %+v, want exactly Delray Beach, Florida, United States", delray)
		}
	})

	t.Run("picker round-trip: full name finds itself", func(t *testing.T) {
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
	})

	t.Run("prefix lists all matches with unique FullNames, no duplicates", func(t *testing.T) {
		for _, prefix := range []string{"Hyderabad", "Banjar"} {
			all, err := r.SearchByName(ctx, prefix, 8)
			if err != nil {
				t.Fatalf("SearchByName(%s): %v", prefix, err)
			}
			seen := map[string]bool{}
			for _, m := range all {
				if seen[m.FullName] {
					t.Errorf("%s: duplicate FullName %q", prefix, m.FullName)
				}
				seen[m.FullName] = true
			}
			if len(seen) < 2 {
				t.Errorf("%s: expected several matches, got %v", prefix, seen)
			}
		}
	})
}

// TestCandidatesDisambiguatesRealDB checks the three-tier DisplayName ladder
// against real gazetteer rows: unique name → bare, repeats in-country →
// state, repeats only abroad → country.
func TestCandidatesDisambiguatesRealDB(t *testing.T) {
	r := locationtest.Resolver(t)
	ctx := context.Background()

	tests := []struct {
		name           string
		lat, lon       float64
		want, wantFull string
	}{
		{"Shimla", 31.10442, 77.16662, "Shimla", "Shimla, Himachal Pradesh, India"},
		{"Hyderabad", 17.38405, 78.45636, "Hyderabad, India", "Hyderabad, Telangana, India"},
		{"Springfield", 39.80172, -89.64371, "Springfield, Illinois", "Springfield, Illinois, United States"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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
				t.Errorf("DisplayName = %q, want %q (cands=%+v)", got, tc.want, cands)
			}
			if gotFull != tc.wantFull {
				t.Errorf("FullName = %q, want %q", gotFull, tc.wantFull)
			}
		})
	}
}
