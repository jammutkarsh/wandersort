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
	matches := []location.PlaceMatch{{Name: "Delhi"}, {Name: "Dehradun"}}

	if name, ok := exactMatch(matches, "delhi"); !ok || name != "Delhi" {
		t.Errorf("case-insensitive match should return canonical spelling, got %q ok=%v", name, ok)
	}
	if _, ok := exactMatch(matches, "Del"); ok {
		t.Errorf("prefix should not be an exact match")
	}
}
