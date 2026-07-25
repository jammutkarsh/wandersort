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

func TestExactMatch(t *testing.T) {
	matches := []location.PlaceMatch{
		{Name: "Delhi", DisplayName: "Delhi"},
		{Name: "Dehradun", DisplayName: "Dehradun"},
	}

	if name, ok := exactMatch(matches, "delhi"); !ok || name != "Delhi" {
		t.Errorf("case-insensitive match should return canonical spelling, got %q ok=%v", name, ok)
	}
	if _, ok := exactMatch(matches, "Del"); ok {
		t.Errorf("prefix should not be an exact match")
	}
}

// A name shared by several cities is offered — and must be saved — with its
// qualifier: the bare city resolves to whichever row the DB returns first,
// which is how a home town in India became one in Pakistan.
func TestExactMatchKeepsQualifier(t *testing.T) {
	matches := []location.PlaceMatch{
		{Name: "Hyderabad", DisplayName: "Hyderabad, Pakistan"},
		{Name: "Hyderabad", DisplayName: "Hyderabad, India"},
	}

	if name, ok := exactMatch(matches, "Hyderabad, India"); !ok || name != "Hyderabad, India" {
		t.Errorf("picked qualified name = %q ok=%v, want \"Hyderabad, India\"", name, ok)
	}
	// Typing the bare name still matches, and still saves a qualified form —
	// an unqualified anchor can't be resolved back to one row.
	if name, ok := exactMatch(matches, "hyderabad"); !ok || name != "Hyderabad, Pakistan" {
		t.Errorf("bare name = %q ok=%v, want the first match's qualified name", name, ok)
	}
}
