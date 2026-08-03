// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package location

import (
	"reflect"
	"testing"
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
		got := disambiguate(tc.city, tc.state, tc.country, tc.nameCnt, tc.countryCnt, tc.inCountryCnt, false, ", ")
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
// skipping whatever the geonames database doesn't have.
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

// A name shared by several cities is offered — and must be saved — with its
// qualifier: the bare city resolves to whichever row the DB returns first,
// which is how a home town in India became one in Pakistan.
func TestExactMatch(t *testing.T) {
	simple := []PlaceMatch{
		{Name: "Delhi", DisplayName: "Delhi"},
		{Name: "Dehradun", DisplayName: "Dehradun"},
	}
	qualified := []PlaceMatch{
		{Name: "Hyderabad", DisplayName: "Hyderabad, Pakistan"},
		{Name: "Hyderabad", DisplayName: "Hyderabad, India"},
	}

	tests := []struct {
		name    string
		matches []PlaceMatch
		typed   string
		want    string
		wantOK  bool
	}{
		{"case-insensitive returns canonical spelling", simple, "delhi", "Delhi", true},
		{"prefix is not an exact match", simple, "Del", "", false},
		{"qualified typed name keeps its qualifier", qualified, "Hyderabad, India", "Hyderabad, India", true},
		// Typing the bare name still matches, and still saves a qualified
		// form — an unqualified anchor can't be resolved back to one row.
		{"bare name resolves to first match's qualified name", qualified, "hyderabad", "Hyderabad, Pakistan", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := exactMatch(tt.matches, tt.typed)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("exactMatch(%q) = (%q, %v), want (%q, %v)", tt.typed, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
