// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// External test package: drives ResolveByName/Canonical/SuggestNames/BuildAnchors
// against the real geonames database through installtest, same as resolver_test.go.
package location_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
	"github.com/jammutkarsh/wandersort/pkg/location"
)

// TestResolveByNameHonoursQualifiers is the bug the package doc calls out: a
// bare "Hyderabad" could resolve to whichever row the DB returns first, which
// is how a home town in India became one in Pakistan. A qualified name must
// resolve to the matching row, not just any row with that city name.
func TestResolveByNameHonoursQualifiers(t *testing.T) {
	r := installtest.Resolver(t)
	ctx := context.Background()

	lat, lon, err := r.ResolveByName(ctx, "Hyderabad, Telangana, India")
	if err != nil {
		t.Fatalf("ResolveByName(India): %v", err)
	}
	if !closeTo(lat, 17.38405) || !closeTo(lon, 78.45636) {
		t.Errorf("Hyderabad, India resolved to (%.5f,%.5f), want the Telangana row (17.38405,78.45636)", lat, lon)
	}

	lat, lon, err = r.ResolveByName(ctx, "Hyderabad, Sindh, Pakistan")
	if err != nil {
		t.Fatalf("ResolveByName(Pakistan): %v", err)
	}
	if !closeTo(lat, 25.39689) || !closeTo(lon, 68.37718) {
		t.Errorf("Hyderabad, Pakistan resolved to (%.5f,%.5f), want the Sindh row (25.39689,68.37718)", lat, lon)
	}
}

// TestResolveByNameUnknownPlace covers the miss path: a name with no geonames
// row at all, qualified or not, must return ErrNoLocation rather than a
// zero-value coordinate that looks like a real match.
func TestResolveByNameUnknownPlace(t *testing.T) {
	r := installtest.Resolver(t)
	if _, _, err := r.ResolveByName(context.Background(), "Nonexistentville12345"); !errors.Is(err, location.ErrNoLocation) {
		t.Errorf("ResolveByName(unknown) err = %v, want ErrNoLocation", err)
	}
}

// TestResolveByNameFallsBackToStrippedDiacritics is resolveStripped's job:
// this package only ever hands out diacritic-stripped names (stripDiacritics),
// so a saved anchor like "Lavasan" must still resolve even though the
// database itself stores the marked spelling ("Lavāsān").
func TestResolveByNameFallsBackToStrippedDiacritics(t *testing.T) {
	r := installtest.Resolver(t)
	lat, lon, err := r.ResolveByName(context.Background(), "Lavasan, Tehran, Iran")
	if err != nil {
		t.Fatalf("ResolveByName(stripped): %v", err)
	}
	if !closeTo(lat, 35.82159) || !closeTo(lon, 51.64444) {
		t.Errorf("Lavasan resolved to (%.5f,%.5f), want (35.82159,51.64444)", lat, lon)
	}
}

// TestCanonicalExactMatchRoundTrips is Canonical's happy path: a name that
// exactly matches one geonames row comes back as the fullest spelling that
// row supports (canonicalNameOf) — the write side of ResolveByName, so what
// it saves must resolve back to the same row.
func TestCanonicalExactMatchRoundTrips(t *testing.T) {
	r := installtest.Resolver(t)
	ctx := context.Background()

	got, err := r.Canonical(ctx, "Indore")
	if err != nil {
		t.Fatalf("Canonical(Indore): %v", err)
	}
	if got != "Indore, Madhya Pradesh, India" {
		t.Errorf("Canonical(Indore) = %q, want the full spelled-out form", got)
	}

	// round-trip: what Canonical saves, ResolveByName must resolve
	lat, lon, err := r.ResolveByName(ctx, got)
	if err != nil {
		t.Fatalf("ResolveByName(%q): %v", got, err)
	}
	if !closeTo(lat, 22.71792) || !closeTo(lon, 75.8333) {
		t.Errorf("resolved (%.5f,%.5f), want Indore's coordinates", lat, lon)
	}
}

// TestCanonicalRejectsNearMiss is Canonical's other job: typing a bare prefix
// that several rows share, without the qualifier that picks one, must fail
// loudly (naming the closest alternative) rather than silently save an
// ambiguous spelling that would anchor the library to whichever row it later
// happens to resolve to.
func TestCanonicalRejectsNearMiss(t *testing.T) {
	r := installtest.Resolver(t)
	_, err := r.Canonical(context.Background(), "Hyderab")
	if err == nil {
		t.Fatal("Canonical(ambiguous prefix) = nil error, want a did-you-mean error")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("Canonical(ambiguous prefix) error = %q, want it to suggest an alternative", err.Error())
	}
}

// TestCanonicalUnknownPlaceReturnsUnchanged is the "geonames never heard of
// this village" case: SearchByName returns nothing at all, and Canonical must
// not treat an empty result as a rejection — an unlisted place must still be
// usable, or the wizard's town field couldn't accept it.
func TestCanonicalUnknownPlaceReturnsUnchanged(t *testing.T) {
	r := installtest.Resolver(t)
	got, err := r.Canonical(context.Background(), "Nonexistentville12345")
	if err != nil {
		t.Fatalf("Canonical(unknown): %v", err)
	}
	if got != "Nonexistentville12345" {
		t.Errorf("Canonical(unknown) = %q, want the typed name unchanged", got)
	}
}

// TestCanonicalBlankInput and a nil receiver are the guard clauses at the top
// of Canonical.
func TestCanonicalBlankInput(t *testing.T) {
	r := installtest.Resolver(t)
	if _, err := r.Canonical(context.Background(), "   "); err == nil {
		t.Error("Canonical(blank) = nil error, want an error")
	}
}

func TestCanonicalNilResolver(t *testing.T) {
	var r *location.Resolver
	if _, err := r.Canonical(context.Background(), "Indore"); err == nil {
		t.Error("Canonical on nil resolver = nil error, want an error")
	}
}

// TestSuggestNamesReturnsFullNames pins that the picker sees qualified full
// names, not bare city names — six identical "Hyderabad"s would be unpickable.
func TestSuggestNamesReturnsFullNames(t *testing.T) {
	r := installtest.Resolver(t)
	names := r.SuggestNames(context.Background(), "Hyderabad", 8)
	if len(names) == 0 {
		t.Fatal("SuggestNames(Hyderabad) = empty, want at least one match")
	}
	for _, n := range names {
		if !strings.Contains(n, ",") {
			t.Errorf("SuggestNames returned %q, want a qualified full name (city repeats across countries)", n)
		}
	}
}

// TestBuildAnchorsSkipsUnresolvableNames is BuildAnchors' warning path: a
// saved place that no longer resolves must be dropped, not left as a
// zero-coordinate anchor that would then silently claim (0,0) as a real spot.
func TestBuildAnchorsSkipsUnresolvableNames(t *testing.T) {
	r := installtest.Resolver(t)
	anchors := r.BuildAnchors(context.Background(), []string{"Indore, Madhya Pradesh, India", "Nonexistentville12345"})
	if len(anchors) != 1 {
		t.Fatalf("BuildAnchors = %d anchors, want 1 (the unresolvable name must be skipped)", len(anchors))
	}
	if anchors[0].Name != "Indore, Madhya Pradesh, India" {
		t.Errorf("anchors[0].Name = %q, want the resolvable one kept", anchors[0].Name)
	}
	if anchors[0].FolderName != "Indore" {
		t.Errorf("anchors[0].FolderName = %q, want %q (unique among anchors, no qualifier needed)", anchors[0].FolderName, "Indore")
	}
}

// TestBuildAnchorsDisambiguatesSharedCityNames is BuildAnchors' own
// disambiguation ladder (separate code path from Candidates/SearchByName's):
// two anchors sharing a bare city name must each get a qualified FolderName so
// neither's folder silently claims the other's.
func TestBuildAnchorsDisambiguatesSharedCityNames(t *testing.T) {
	r := installtest.Resolver(t)
	anchors := r.BuildAnchors(context.Background(), []string{
		"Hyderabad, Telangana, India",
		"Hyderabad, Sindh, Pakistan",
	})
	if len(anchors) != 2 {
		t.Fatalf("BuildAnchors = %d anchors, want 2", len(anchors))
	}
	for _, a := range anchors {
		if a.FolderName == "Hyderabad" {
			t.Errorf("anchor %q FolderName = %q, want a qualifier — two anchors share this bare city", a.Name, a.FolderName)
		}
		if strings.Contains(a.FolderName, ",") {
			t.Errorf("anchor %q FolderName = %q, want the comma-free ' - ' separator", a.Name, a.FolderName)
		}
	}
	if anchors[0].FolderName == anchors[1].FolderName {
		t.Errorf("both anchors got FolderName %q, want them distinguishable", anchors[0].FolderName)
	}
}

// TestBuildAnchorsNilResolver is the nil-safety guard: a config with no
// location database yet (offline first run) must not panic when the vfs phase
// still calls BuildAnchors on a nil *Resolver.
func TestBuildAnchorsNilResolver(t *testing.T) {
	var r *location.Resolver
	if got := r.BuildAnchors(context.Background(), []string{"Indore"}); got != nil {
		t.Errorf("BuildAnchors on nil resolver = %v, want nil", got)
	}
}

func closeTo(a, b float64) bool {
	const eps = 1e-4
	d := a - b
	return d > -eps && d < eps
}
