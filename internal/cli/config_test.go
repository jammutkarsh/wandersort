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
			a := &App{Config: cfg}

			fields, save := a.buildConfigForm(context.Background(), func() error { return errGazetteerPending })

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
			brokenFields, brokenSave := a.buildConfigForm(context.Background(), func() error { return broken })
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

			warning, err := loadGlobalConfigFile()
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

			warning, err := loadGlobalConfigFile()
			if err != nil || warning != "" {
				t.Fatalf("valid config: err=%v warning=%q", err, warning)
			}
			if got := v.GetInt(flagWorkers); got != 7 {
				t.Errorf("workers = %d, want 7 from the config file", got)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}
