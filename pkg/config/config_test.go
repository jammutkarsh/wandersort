// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestGlobal(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"EnsureGlobalConfigFileDoesNotClobber", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			cfg, err := defaults()
			if err != nil {
				t.Fatalf("defaults: %v", err)
			}
			path, err := cfg.Exists()
			if err != nil {
				t.Fatalf("EnsureGlobalConfigFile: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if len(data) != 0 {
				t.Fatalf("a fresh config file must start empty, got:\n%s", data)
			}

			// a second call must not clobber a file that's since been written
			if err := os.WriteFile(path, []byte("workers: 8\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := cfg.Exists(); err != nil {
				t.Fatalf("EnsureGlobalConfigFile (existing): %v", err)
			}
			data, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "workers: 8") {
				t.Error("EnsureGlobalConfigFile overwrote an existing file")
			}
		}},
		{"LoadGlobalOnMissingFile", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			cfg, err := defaults()
			if err != nil {
				t.Fatalf("defaults: %v", err)
			}
			g, err := cfg.Load()
			if err != nil || g.OutputPath != "" {
				t.Fatalf("LoadGlobal on missing file = (%+v, %v), want (zero output-path, nil)", g, err)
			}
		}},
		// TestSaveGlobalRoundTrip is the one that matters: every setting the config
		// wizard collects has to survive the file, including the false bools (which
		// must be written, not omitted as "unset") and the home/work names.
		{"SaveGlobalRoundTrip", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			cfg, err := defaults()
			if err != nil {
				t.Fatalf("defaults: %v", err)
			}
			want := &Configuration{
				OutputPath:            "/tmp/lib",
				Workers:               8,
				Rules:                 []string{"date", "location"},
				CollapseLevels:        false,
				HomeWorkDateOnly:      false,
				MergeSameLocationDays: true,
				SavedPlaces:           []string{"Delhi", "Gurugram"},
			}
			if err := cfg.Save(want); err != nil {
				t.Fatalf("SaveGlobal: %v", err)
			}
			got, err := cfg.Load()
			if err != nil {
				t.Fatalf("LoadGlobal: %v", err)
			}
			if !overridesEqual(got, want) {
				t.Fatalf("round trip = %+v, want %+v", got, want)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

func overridesEqual(o Overrides, c *Configuration) bool {
	return o.OutputPath == c.OutputPath &&
		o.Workers == c.Workers &&
		reflect.DeepEqual(o.Rules, c.Rules) &&
		triToBool(o.CollapseLevels) == c.CollapseLevels &&
		triToBool(o.HomeWorkDateOnly) == c.HomeWorkDateOnly &&
		triToBool(o.MergeSameLocationDays) == c.MergeSameLocationDays &&
		reflect.DeepEqual(o.SavedPlaces, c.SavedPlaces)
}

func triToBool(t TriBool) bool { return t == True }

// TestResolve covers the precedence chain (flag > env > file > default) —
// the one thing worth a real test now that it's concentrated in one
// function instead of split across viper binding and applyOverrides.
func TestResolve(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"DefaultsOnlyLeavesUnconfigured", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			cfg, warning, err := Resolve(Overrides{})
			if err != nil || warning != "" {
				t.Fatalf("Resolve: err=%v warning=%q", err, warning)
			}
			if cfg.Configured {
				t.Error("no flag/env/file set output-path — Configured must be false")
			}
			if !cfg.CollapseLevels || !cfg.HomeWorkDateOnly || !cfg.MergeSameLocationDays {
				t.Errorf("unconfigured bools must keep their hardcoded defaults, got %+v", cfg)
			}
		}},
		{"FileOverridesDefault", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			c, err := defaults()
			if err != nil {
				t.Fatalf("defaults: %v", err)
			}
			if err := c.Save(&Configuration{OutputPath: "/tmp/from-file", Workers: 3}); err != nil {
				t.Fatal(err)
			}

			cfg, _, err := Resolve(Overrides{})
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.Configured {
				t.Error("file set output-path — Configured must be true")
			}
			if cfg.Workers != 3 {
				t.Errorf("workers = %d, want 3 from the file", cfg.Workers)
			}
		}},
		{"EnvOverridesFile", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			c, err := defaults()
			if err != nil {
				t.Fatalf("defaults: %v", err)
			}
			if err := c.Save(&Configuration{OutputPath: "/tmp/from-file", Workers: 3}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("WORKERS", "9")

			cfg, _, err := Resolve(Overrides{})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Workers != 9 {
				t.Errorf("workers = %d, want 9 from the env var", cfg.Workers)
			}
		}},
		{"FlagOverridesEnvAndFile", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			c, err := defaults()
			if err != nil {
				t.Fatalf("defaults: %v", err)
			}
			if err := c.Save(&Configuration{OutputPath: "/tmp/from-file", Workers: 3}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("WORKERS", "9")

			cfg, _, err := Resolve(Overrides{Workers: 5})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Workers != 5 {
				t.Errorf("workers = %d, want 5 from the flag", cfg.Workers)
			}
		}},
		{"FlagOutputPathAloneIsConfigured", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			cfg, _, err := Resolve(Overrides{OutputPath: "/tmp/from-flag"})
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.Configured {
				t.Error("--output-path alone must count as configured")
			}
		}},
		{"BadYAMLFallsBackWithWarning", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			c, err := defaults()
			if err != nil {
				t.Fatalf("defaults: %v", err)
			}
			path, err := c.Exists()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("workers: [unclosed\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			cfg, warning, err := Resolve(Overrides{})
			if err != nil {
				t.Fatalf("bad YAML must not be fatal, got %v", err)
			}
			if warning == "" {
				t.Error("expected a warning naming the unparseable file")
			}
			if cfg.Configured {
				t.Error("a file that failed to parse must not count as configured")
			}
		}},
		{"UnconfiguredFileDoesNotForceBoolsFalse", func(t *testing.T) {
			// A file with only workers set (never through the wizard, so no
			// output-path) must not stomp CollapseLevels/HomeWorkDateOnly/
			// MergeSameLocationDays to their Go zero value (false).
			t.Setenv("HOME", t.TempDir())
			c, err := defaults()
			if err != nil {
				t.Fatalf("defaults: %v", err)
			}
			if err := c.Save(&Configuration{Workers: 4}); err != nil {
				t.Fatal(err)
			}

			cfg, _, err := Resolve(Overrides{})
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.CollapseLevels || !cfg.HomeWorkDateOnly || !cfg.MergeSameLocationDays {
				t.Errorf("bools must keep their defaults when output-path was never set, got %+v", cfg)
			}
		}},
		{"FileBoolsApplyOnceConfigured", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			c, err := defaults()
			if err != nil {
				t.Fatalf("defaults: %v", err)
			}
			if err := c.Save(&Configuration{
				OutputPath:     "/tmp/from-file",
				CollapseLevels: false,
			}); err != nil {
				t.Fatal(err)
			}

			cfg, _, err := Resolve(Overrides{})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.CollapseLevels {
				t.Error("explicit collapse-levels: false in a configured file must be honoured")
			}
		}},
		{"FlagBoolOverridesFile", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			c, err := defaults()
			if err != nil {
				t.Fatalf("defaults: %v", err)
			}
			if err := c.Save(&Configuration{OutputPath: "/tmp/from-file", CollapseLevels: false}); err != nil {
				t.Fatal(err)
			}

			cfg, _, err := Resolve(Overrides{CollapseLevels: True})
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.CollapseLevels {
				t.Error("flag override must win over the file's false")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}
