// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package location

import (
	"context"
	"testing"
)

// A nil Resolver skips only the database-backed source, so the in-memory
// ranking is testable without a geonames database.
func TestSuggestRanksAndDedupes(t *testing.T) {
	nearby := []Candidate{
		{Name: "Indore", FullName: "Indore, Madhya Pradesh, India", FolderName: "Indore", DistKM: 3},
		{Name: "Dewas", FullName: "Dewas, Madhya Pradesh, India", FolderName: "Dewas", DistKM: 34},
	}

	t.Run("no prefix offers nearby only", func(t *testing.T) {
		got := (*Resolver)(nil).Suggest(context.Background(), SuggestQuery{
			Nearby: nearby,
			Prior:  []string{"Goa Trip"},
		})
		if len(got) != 2 || got[0].Label != "Indore, Madhya Pradesh, India" || got[1].Value != "Dewas" {
			t.Fatalf("Suggest(no prefix) = %+v, want the two nearby places, nearest first", got)
		}
		if got[0].Detail != "~3km" {
			t.Errorf("nearby detail = %q, want a distance", got[0].Detail)
		}
	})

	t.Run("prefix filters nearby and adds prior names", func(t *testing.T) {
		got := (*Resolver)(nil).Suggest(context.Background(), SuggestQuery{
			Prefix: "de",
			Nearby: nearby,
			Prior:  []string{"Dewas Weekend", "Goa Trip"},
		})
		if len(got) != 2 {
			t.Fatalf("Suggest(%q) = %+v, want 2", "de", got)
		}
		if got[0].Value != "Dewas" || got[1].Detail != "used before" {
			t.Errorf("Suggest(%q) = %+v, want the nearby match first then the prior name", "de", got)
		}
	})

	t.Run("a prior name is sanitized into a folder name", func(t *testing.T) {
		got := (*Resolver)(nil).Suggest(context.Background(), SuggestQuery{
			Prefix: "goa",
			Prior:  []string{"Goa / Trip 2024"},
		})
		if len(got) != 1 || got[0].Value != "Goa-Trip-2024" {
			t.Fatalf("Suggest(prior with a slash) = %+v, want a folder-safe value", got)
		}
		if got[0].Label != "Goa / Trip 2024" {
			t.Errorf("label = %q, want the name as typed", got[0].Label)
		}
	})

	t.Run("two sources naming one folder collapse to one row", func(t *testing.T) {
		got := (*Resolver)(nil).Suggest(context.Background(), SuggestQuery{
			Prefix: "indore",
			Nearby: nearby,
			Prior:  []string{"Indore"},
		})
		if len(got) != 1 {
			t.Fatalf("Suggest = %+v, want the duplicate folder name offered once", got)
		}
	})

	t.Run("limit caps the list", func(t *testing.T) {
		prior := []string{"Trip A", "Trip B", "Trip C"}
		got := (*Resolver)(nil).Suggest(context.Background(), SuggestQuery{
			Prefix: "trip", Prior: prior, Limit: 2,
		})
		if len(got) != 2 {
			t.Fatalf("Suggest(limit 2) returned %d rows", len(got))
		}
	})
}
