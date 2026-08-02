// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// External test package: it drives the Resolver against the real geonames
// database through installtest, which imports this package back.
package location_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
	"github.com/jammutkarsh/wandersort/pkg/location"
)

// TestCandidatesQualifyRepeatedNames pins what the deferred name counts are
// for: the qualifier ladder still sees the whole database, not just the rows
// the bounding box returned. Hyderabad exists in India and Pakistan, so a
// lookup near either must not hand back a bare "Hyderabad".
func TestCandidatesQualifyRepeatedNames(t *testing.T) {
	r := installtest.Resolver(t)
	ctx := context.Background()

	cands, err := r.Candidates(ctx, 17.3850, 78.4867, location.NearSearchDegrees, 8)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	var found *location.Candidate
	for i := range cands {
		if cands[i].Name == "Hyderabad" {
			found = &cands[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no Hyderabad among %d candidates near its own coordinates", len(cands))
	}
	if found.DisplayName == found.Name {
		t.Errorf("DisplayName = %q, want a qualifier — the name repeats across countries", found.DisplayName)
	}
	if found.FullName == found.Name {
		t.Errorf("FullName = %q, want city, state and country spelled out", found.FullName)
	}
}

// TestLookupCachesMisses covers the negative cache: a coordinate in the middle
// of the Pacific has no city inside the acceptance radius, and asking twice
// must not query twice. A cancelled context on the second call is the proof —
// it can only return ErrNoLocation by never reaching SQL.
func TestLookupCachesMisses(t *testing.T) {
	r := installtest.Resolver(t)

	const lat, lon = 0.0, -140.0
	if _, err := r.Lookup(context.Background(), lat, lon); !errors.Is(err, location.ErrNoLocation) {
		t.Fatalf("first Lookup err = %v, want ErrNoLocation", err)
	}

	dead, cancel := context.WithCancel(context.Background())
	cancel()
	// same square, jittered well inside the cache grid
	if _, err := r.Lookup(dead, lat+0.001, lon-0.001); !errors.Is(err, location.ErrNoLocation) {
		t.Errorf("second Lookup err = %v, want the cached ErrNoLocation (it queried again)", err)
	}
}

// TestLookupCachesHits is the same proof for a resolved coordinate.
func TestLookupCachesHits(t *testing.T) {
	r := installtest.Resolver(t)

	const lat, lon = 15.5439, 73.7553 // Calangute
	city, err := r.Lookup(context.Background(), lat, lon)
	if err != nil {
		t.Fatalf("first Lookup: %v", err)
	}

	dead, cancel := context.WithCancel(context.Background())
	cancel()
	// jitter small enough to stay in the same square wherever inside it the
	// first reading sat — these coordinates are near a grid boundary
	got, err := r.Lookup(dead, lat+0.0001, lon+0.0001)
	if err != nil {
		t.Fatalf("second Lookup err = %v, want the cached hit (it queried again)", err)
	}
	if got != city {
		t.Errorf("second Lookup = %q, want %q", got, city)
	}
}

// benchCoords mixes dense metros (where the bounding box returns the full
// candidateFetchLimit) with sparser towns, since the row count is what the
// name-count query scales with.
var benchCoords = [][2]float64{
	{48.8566, 2.3522},   // Paris
	{40.7128, -74.0060}, // New York
	{22.7196, 75.8577},  // Indore
	{28.6139, 77.2090},  // Delhi
	{19.0760, 72.8777},  // Mumbai
	{15.5439, 73.7553},  // Calangute
}

// BenchmarkLookupMiss times an uncached reverse-geocode — the VFS phase's only
// real cost. A fresh Resolver per iteration keeps every lookup a cache miss.
func BenchmarkLookupMiss(b *testing.B) {
	r := installtest.Resolver(b)
	ctx := context.Background()
	round := 0
	for b.Loop() {
		// walk every coordinate into a cache square no iteration has used, or
		// this times the sync.Map instead of the query
		offset := float64(round) * 0.02
		for _, c := range benchCoords {
			//nolint:errcheck // a miss is a valid outcome to time
			r.Lookup(ctx, c[0]+offset, c[1])
		}
		round++
	}
}

// BenchmarkCandidates times the review TUI's rename picker, which asks for the
// full candidate list rather than the single nearest name.
func BenchmarkCandidates(b *testing.B) {
	r := installtest.Resolver(b)
	ctx := context.Background()
	for b.Loop() {
		for _, c := range benchCoords {
			if _, err := r.Candidates(ctx, c[0], c[1], location.NearSearchDegrees, 64); err != nil {
				b.Fatal(err)
			}
		}
	}
}
