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
