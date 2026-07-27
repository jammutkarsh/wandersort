// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/location/locationtest"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// fieldByTitle finds a form field by its title so the tests don't pin the
// wizard's field order.
func fieldByTitle(t *testing.T, fields []*tui.Field, title string) *tui.Field {
	t.Helper()
	for _, f := range fields {
		if f.Title == title {
			return f
		}
	}
	t.Fatalf("no %q field in the wizard", title)
	return nil
}

func TestConfig(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// TestConfigFormSavesEverySetting covers the wizard's write path: submitting
		// the form must persist every setting it collects, and a town typed before the
		// location database finished downloading must be rejected rather than saved
		// unvalidated.
		{"ConfigFormSavesEverySetting", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)

			cfg, err := config.Defaults()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Workers = 3
			cfg.Rules = []string{"date", "device"}
			cfg.CollapseLevels = false
			a := &app{Config: cfg}

			fields, save := a.buildConfigForm(context.Background(), func() (*location.Resolver, error) { return nil, errGazetteerPending })

			// The two folder questions belong to the home/work step, after the towns:
			// their examples read off the town typed one field earlier.
			group := fieldByTitle(t, fields, "Home & work")
			var subTitles []string
			for _, sub := range group.Subs {
				subTitles = append(subTitles, sub.Title)
			}
			wantSubs := []string{"Home town", "Work town", "Collapse uninformative levels?", "Group home/work photos by date only?", "Merge consecutive same-location days?"}
			if !reflect.DeepEqual(subTitles, wantSubs) {
				t.Errorf("home/work step = %v, want %v", subTitles, wantSubs)
			}

			// Collapse always demonstrates all three collapsible levels, regardless
			// of whether Rules currently has device/orientation/media ticked — this
			// test's cfg.Rules is only date+device, so location/orientation/media
			// would otherwise be silently skipped.
			collapseField := group.Subs[2]
			*collapseField.BoolValue = false
			if ex := collapseField.Example(); !strings.Contains(ex, "iPhone 13") || !strings.Contains(ex, "Vertical") || !strings.Contains(ex, "Photos") {
				t.Errorf("collapse example must show all three collapsible levels even when unticked in Rules, got %q", ex)
			}
			*collapseField.BoolValue = true
			if ex := collapseField.Example(); strings.Contains(ex, "iPhone 13") {
				t.Errorf("collapsed example must drop the collapsible levels, got %q", ex)
			}
			*collapseField.BoolValue = false // restore: cfg.CollapseLevels started false

			// The step holds while the database downloads — the wizard says so instead
			// of failing a validation the user can't fix.
			if group.Await == nil || group.Await() == "" {
				t.Error("home/work step must wait while the location database downloads")
			}

			// Examples live outside the description, and only the active choice's.
			dateOnly := group.Subs[3]
			if strings.Contains(dateOnly.Description, "2024/") {
				t.Errorf("example must not be inside the description: %q", dateOnly.Description)
			}
			*dateOnly.BoolValue = true
			if ex := dateOnly.Example(); strings.Contains(ex, "Indore") {
				t.Errorf("date-only example must drop the town folder, got %q", ex)
			}
			*dateOnly.BoolValue = false
			if ex := dateOnly.Example(); !strings.Contains(ex, "Indore") {
				t.Errorf("date-off example must show the town folder, got %q", ex)
			}

			townField := group.Subs[0]
			if err := townField.Validator("  "); err != nil {
				t.Errorf("blank town must stay skippable, got %v", err)
			}

			// A gazetteer that never opened (failed download, database busy) must not
			// trap the user on the field — nor drop the town they already had.
			broken := errors.New("location db: database is locked")
			brokenFields, brokenSave := a.buildConfigForm(context.Background(), func() (*location.Resolver, error) { return nil, broken })
			brokenGroup := fieldByTitle(t, brokenFields, "Home & work")
			*brokenGroup.Subs[0].Value = "Indore"
			if err := brokenGroup.Subs[0].Validator("Indore"); err != nil {
				t.Errorf("unusable gazetteer must let a town through, got %v", err)
			}
			if err := brokenSave(); err != nil {
				t.Fatalf("save (broken gazetteer): %v", err)
			}
			if g, _ := config.LoadGlobal(); g.HomeWork.Home != "Indore" || g.HomeWork.Work != "Indore" {
				t.Errorf("home/work = %+v, want the typed town kept (work defaults to home)", g.HomeWork)
			}

			if err := save(); err != nil {
				t.Fatalf("save: %v", err)
			}
			got, err := config.LoadGlobal()
			if err != nil {
				t.Fatalf("LoadGlobal: %v", err)
			}
			want := config.Global{
				OutputPath:       filepath.Dir(cfg.AppDBPath),
				Workers:          3,
				Rules:            []string{"date", "device"},
				CollapseLevels:   false,
				HomeWorkDateOnly: false, // the example checks above left it off

				MergeSameLocationDays: cfg.MergeSameLocationDays,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("saved config = %+v, want %+v", got, want)
			}
		}},
		// TestTownFieldsRoundTripARealTown exercises the real gazetteer path that
		// every other case in this file leaves untouched by passing a nil
		// resolver: with a ready resolver and gazetteer() reporting no error,
		// suggestTown/townValidator/canonicalTown must round-trip a real
		// town through SearchByName/exactMatch to its canonical gazetteer spelling.
		{"TownFieldsRoundTripARealTown", func(t *testing.T) {
			// Resolve against the real machine's ~/.wandersort/location.db
			// *before* HOME gets redirected below — locationtest.Resolver reads
			// $HOME itself, and pointing it at a fresh temp dir would make it
			// re-download the ~80MB database instead of reusing the copy
			// already cached on this machine.
			resolver := locationtest.Resolver(t)

			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)

			cfg, err := config.Defaults()
			if err != nil {
				t.Fatal(err)
			}
			a := &app{Config: cfg}

			fields, save := a.buildConfigForm(context.Background(), func() (*location.Resolver, error) { return resolver, nil })
			group := fieldByTitle(t, fields, "Home & work")
			homeField := group.Subs[0]

			// A partial real city name must surface a real gazetteer entry — the
			// full "city, state, country" form the picker always lists.
			suggestions := homeField.Suggest("Indo")
			found := false
			for _, s := range suggestions {
				if s == "Indore, Madhya Pradesh, India" {
					found = true
				}
			}
			if !found {
				t.Errorf("suggestions for %q = %v, want to include %q", "Indo", suggestions, "Indore, Madhya Pradesh, India")
			}

			// A typed name the gazetteer knows validates clean...
			if err := homeField.Validator("indore"); err != nil {
				t.Errorf("known town must validate, got %v", err)
			}
			// ...and a name it has never heard of is accepted as typed — the
			// gazetteer missing a village must not trap the user on this field.
			if err := homeField.Validator("Nowhereville Not A Real Town"); err != nil {
				t.Errorf("unknown town must validate as typed, got %v", err)
			}
			if got, err := canonicalTown(context.Background(), resolver, "Nowhereville Not A Real Town"); err != nil || got != "Nowhereville Not A Real Town" {
				t.Errorf("canonicalTown(unknown) = %q, %v, want the typed name back", got, err)
			}

			// canonicalTown resolves the typed spelling to the gazetteer's own — the
			// full "city, state, country" form, per canonicalNameOf.
			got, err := canonicalTown(context.Background(), resolver, "indore")
			if err != nil {
				t.Fatalf("canonicalTown(%q): %v", "indore", err)
			}
			if want := "Indore, Madhya Pradesh, India"; got != want {
				t.Errorf("canonicalTown(%q) = %q, want %q", "indore", got, want)
			}

			*homeField.Value = "indore"
			if err := save(); err != nil {
				t.Fatalf("save: %v", err)
			}
			if g, _ := config.LoadGlobal(); g.HomeWork.Home != "Indore, Madhya Pradesh, India" {
				t.Errorf("saved home town = %q, want the canonical gazetteer spelling", g.HomeWork.Home)
			}
		}},
		// TestBadYAMLWarnsAndFallsBackToDefaults covers the escape hatch: a config
		// file that doesn't parse must not stop the command. Every setting in it is
		// optional, and failing hard would mean a stray tab locks the user out of
		// every command — including the one that opens the file to fix it.
		{"BadYAMLWarnsAndFallsBackToDefaults", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home) // windows
			if err := os.MkdirAll(filepath.Join(home, ".wandersort"), 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, ".wandersort", "config.yaml")
			if err := os.WriteFile(path, []byte("workers: 4\n\tbad: [unclosed\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			_, warning, err := config.Resolve(config.FlagOverrides{})
			if err != nil {
				t.Fatalf("bad YAML must not be a fatal error, got %v", err)
			}
			if warning == "" {
				t.Error("expected a warning naming the unparseable file")
			}
		}},
		// TestValidYAMLIsStillApplied guards the other direction: the warning path
		// must not have broken normal config loading.
		{"ValidYAMLIsStillApplied", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			if err := os.MkdirAll(filepath.Join(home, ".wandersort"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".wandersort", "config.yaml"),
				[]byte("workers: 7\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			cfg, warning, err := config.Resolve(config.FlagOverrides{})
			if err != nil || warning != "" {
				t.Fatalf("valid config: err=%v warning=%q", err, warning)
			}
			if cfg.Workers != 7 {
				t.Errorf("workers = %d, want 7 from the config file", cfg.Workers)
			}
		}},
		// TestTreeExample covers the wizard's example renderer: sibling paths must
		// fold their shared prefix into one branch point (the merge-days "no"
		// example), and the note lands on the last line only.
		{"TreeExample", func(t *testing.T) {
			got := treeExample("(note)",
				"2024/08_August/02/IMG_1.jpg",
				"2024/08_August/03/IMG_2.jpg")
			want := "2024\n" +
				"└─ 08_August\n" +
				"   ├─ 02\n" +
				"   │  └─ IMG_1.jpg\n" +
				"   └─ 03\n" +
				"      └─ IMG_2.jpg\n" +
				"\n" +
				"(note)"
			if got != want {
				t.Errorf("treeExample:\n%s\nwant:\n%s", got, want)
			}
		}},
		// TestScanNeedsConfigFirst covers the gate on scan: the empty
		// config.yaml every command creates must not count as configured, or the
		// first scan builds its whole proposal from defaults.
		{"ScanNeedsConfigFirst", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)

			if _, err := config.EnsureGlobalConfigFile(); err != nil {
				t.Fatal(err)
			}
			cfg, _, err := config.Resolve(config.FlagOverrides{})
			if err != nil {
				t.Fatal(err)
			}
			if err := requireConfigured(&app{Config: cfg}); err == nil {
				t.Error("an empty config.yaml must not count as configured")
			}

			if err := config.SaveGlobal(config.Global{OutputPath: filepath.Join(home, "out")}); err != nil {
				t.Fatal(err)
			}
			cfg, _, err = config.Resolve(config.FlagOverrides{})
			if err != nil {
				t.Fatal(err)
			}
			if err := requireConfigured(&app{Config: cfg}); err != nil {
				t.Errorf("after the wizard saved: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

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
