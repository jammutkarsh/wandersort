// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/location"
)

// A name shared by several cities is offered — and must be saved — with its
// qualifier: the bare city resolves to whichever row the DB returns first,
// which is how a home town in India became one in Pakistan.
func TestExactMatch(t *testing.T) {
	simple := []location.PlaceMatch{
		{Name: "Delhi", DisplayName: "Delhi"},
		{Name: "Dehradun", DisplayName: "Dehradun"},
	}
	qualified := []location.PlaceMatch{
		{Name: "Hyderabad", DisplayName: "Hyderabad, Pakistan"},
		{Name: "Hyderabad", DisplayName: "Hyderabad, India"},
	}

	tests := []struct {
		name    string
		matches []location.PlaceMatch
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
