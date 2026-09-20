// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
)

// TestSettings covers the round trip through a library's own database: a
// library that never saw the wizard reads back the defaults, and a saved
// setting survives — including the false bools, which must be stored, not
// dropped as "unset".
func TestSettings(t *testing.T) {
	ctx := context.Background()
	database := dbtest.New(t)

	got, err := LoadSettings(ctx, database)
	if err != nil {
		t.Fatalf("LoadSettings on a fresh library: %v", err)
	}
	if !got.Equal(DefaultSettings()) {
		t.Fatalf("fresh library = %+v, want the defaults %+v", got, DefaultSettings())
	}

	want := Settings{
		Rules:                 []string{"date", "location"},
		CollapseLevels:        false,
		SavedPlacesDateOnly:   false,
		MergeSameLocationDays: true,
		SavedPlaces:           []string{"Delhi", "Gurugram"},
	}
	if err := SaveSettings(ctx, database, want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if got, err = LoadSettings(ctx, database); err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}

	// Saving again replaces the one row rather than adding a second.
	want.Rules = nil
	if err := SaveSettings(ctx, database, want); err != nil {
		t.Fatalf("SaveSettings (second): %v", err)
	}
	if got, err = LoadSettings(ctx, database); err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if len(got.Rules) != 0 {
		t.Errorf("rules = %v, want empty after the second save", got.Rules)
	}
}

// TestSettingsAreThisLibrarys is the point of the whole ticket: two libraries
// keep their own rules (spec D2).
func TestSettingsAreThisLibrarys(t *testing.T) {
	ctx := context.Background()
	a, b := dbtest.New(t), dbtest.New(t)
	if err := SaveSettings(ctx, a, Settings{Rules: []string{"device"}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveSettings(ctx, b, Settings{Rules: []string{"location"}}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Rules, []string{"device"}) {
		t.Errorf("library A's rules = %v, want its own [device]", got.Rules)
	}
}

func TestSettingsEqual(t *testing.T) {
	base := Settings{Rules: []string{"date"}, CollapseLevels: true, SavedPlaces: []string{"Indore"}}
	same := Settings{Rules: []string{"date"}, CollapseLevels: true, SavedPlaces: []string{"Indore"}}
	if !base.Equal(same) {
		t.Error("identical settings must compare equal — a save that changes nothing throws no plan away")
	}
	for name, other := range map[string]Settings{
		"rules":       {Rules: []string{"location"}, CollapseLevels: true, SavedPlaces: []string{"Indore"}},
		"toggle":      {Rules: []string{"date"}, CollapseLevels: false, SavedPlaces: []string{"Indore"}},
		"savedPlaces": {Rules: []string{"date"}, CollapseLevels: true, SavedPlaces: []string{"Bhopal"}},
	} {
		if base.Equal(other) {
			t.Errorf("a changed %s must not compare equal", name)
		}
	}
}

func TestHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := cfg.History(); got != nil {
		t.Fatalf("history with no file = %v, want nil", got)
	}

	first := filepath.Join(home, "first")
	second := filepath.Join(home, "second")
	for _, dir := range []string{first, second} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := cfg.Remember(dir); err != nil {
			t.Fatalf("Remember: %v", err)
		}
	}
	if got := cfg.History(); !slices.Equal(got, []string{second, first}) {
		t.Errorf("history = %v, want the newest first", got)
	}

	// Opening the older one again moves it to the front, it doesn't duplicate.
	if err := cfg.Remember(first); err != nil {
		t.Fatal(err)
	}
	if got := cfg.History(); !slices.Equal(got, []string{first, second}) {
		t.Errorf("history after re-opening = %v, want [first second]", got)
	}

	// A library that isn't there any more (unplugged drive, deleted folder)
	// is dropped as the list is read.
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	if got := cfg.History(); !slices.Equal(got, []string{first}) {
		t.Errorf("history = %v, want the missing folder dropped", got)
	}
}

func TestHistoryCap(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for i := range maxHistory + 10 {
		dir := filepath.Join(home, fmt.Sprintf("lib%03d", i))
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := cfg.Remember(dir); err != nil {
			t.Fatal(err)
		}
	}
	got := cfg.History()
	if len(got) != maxHistory {
		t.Fatalf("history holds %d entries, want the cap of %d", len(got), maxHistory)
	}
	if got[0] != filepath.Join(home, fmt.Sprintf("lib%03d", maxHistory+9)) {
		t.Errorf("history[0] = %q, want the most recent", got[0])
	}
}

// TestNewOpensTheLastLibrary is why the history exists at all: `wandersort
// review` with no --output-path opens the library the last scan filled.
func TestNewOpensTheLastLibrary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.OutputDir(); got != filepath.Join(home, DefaultLibrary) {
		t.Errorf("first launch = %q, want the default library folder", got)
	}

	lib := filepath.Join(home, "Photos")
	if err := os.Mkdir(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Remember(lib); err != nil {
		t.Fatal(err)
	}
	next, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if got := next.OutputDir(); got != lib {
		t.Errorf("next launch = %q, want the most recently used library %q", got, lib)
	}
}

func TestCheckLibrary(t *testing.T) {
	tests := []struct {
		name    string
		files   []string // nil = folder does not exist
		wantErr bool
	}{
		{"missing folder", nil, false},
		{"empty folder", []string{}, false},
		{"existing library", []string{defaultDBFileName, "2024"}, false},
		{"only OS clutter and a leftover lock", []string{".DS_Store", "Thumbs.db", "desktop.ini", ".wandersort.lock"}, false},
		{"leftover files of an older library", []string{".wandersort.log"}, true},
		{"foreign content", []string{".DS_Store", "Goa Trip"}, true},
		{"backup without its database", []string{".wandersort.db.bak", "2024"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "lib")
			if tt.files != nil {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				for _, f := range tt.files {
					if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := CheckLibrary(dir)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckLibrary() = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), dir) {
				t.Errorf("error %q does not name the folder", err)
			}
			if slices.Contains(tt.files, ".wandersort.db.bak") && (err == nil || !strings.Contains(err.Error(), "wandersort recover")) {
				t.Errorf("error %v does not point at 'wandersort recover'", err)
			}
		})
	}
}
