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

			path, err := EnsureGlobalConfigFile()
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
			if _, err := EnsureGlobalConfigFile(); err != nil {
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

			g, err := LoadGlobal()
			if err != nil || !reflect.DeepEqual(g, Global{}) {
				t.Fatalf("LoadGlobal on missing file = (%+v, %v), want (zero value, nil)", g, err)
			}
		}},
		// TestSaveGlobalRoundTrip is the one that matters: every setting the config
		// wizard collects has to survive the file, including the false bools (which
		// must be written, not omitted as "unset") and the home/work names.
		{"SaveGlobalRoundTrip", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			want := Global{
				OutputPath:            "/tmp/lib",
				Workers:               8,
				Rules:                 []string{"date", "location"},
				CollapseLevels:        false,
				HomeWorkDateOnly:      false,
				MergeSameLocationDays: true,
				HomeWork:              HomeWork{Home: "Delhi", Work: "Gurugram"},
			}
			if err := SaveGlobal(want); err != nil {
				t.Fatalf("SaveGlobal: %v", err)
			}
			got, err := LoadGlobal()
			if err != nil {
				t.Fatalf("LoadGlobal: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip = %+v, want %+v", got, want)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

// TestResolve covers the precedence chain (flag > env > file > default) —
// the one thing worth a real test now that it's concentrated in one
// function instead of split across viper binding and applyOverrides.
func TestResolve(t *testing.T) {
	strPtr := func(s string) *string { return &s }
	intPtr := func(n int) *int { return &n }
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"DefaultsOnlyLeavesUnconfigured", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			cfg, warning, err := Resolve(FlagOverrides{})
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
			if err := SaveGlobal(Global{OutputPath: "/tmp/from-file", Workers: 3}); err != nil {
				t.Fatal(err)
			}

			cfg, _, err := Resolve(FlagOverrides{})
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
			if err := SaveGlobal(Global{OutputPath: "/tmp/from-file", Workers: 3}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("WORKERS", "9")

			cfg, _, err := Resolve(FlagOverrides{})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Workers != 9 {
				t.Errorf("workers = %d, want 9 from the env var", cfg.Workers)
			}
		}},
		{"FlagOverridesEnvAndFile", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if err := SaveGlobal(Global{OutputPath: "/tmp/from-file", Workers: 3}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("WORKERS", "9")

			cfg, _, err := Resolve(FlagOverrides{Workers: intPtr(5)})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Workers != 5 {
				t.Errorf("workers = %d, want 5 from the flag", cfg.Workers)
			}
		}},
		{"FlagOutputPathAloneIsConfigured", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			cfg, _, err := Resolve(FlagOverrides{OutputPath: strPtr("/tmp/from-flag")})
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.Configured {
				t.Error("--output-path alone must count as configured")
			}
		}},
		{"BadYAMLFallsBackWithWarning", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if _, err := EnsureGlobalConfigFile(); err != nil {
				t.Fatal(err)
			}
			path, err := GlobalConfigPath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("workers: [unclosed\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			cfg, warning, err := Resolve(FlagOverrides{})
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
			if err := SaveGlobal(Global{Workers: 4}); err != nil {
				t.Fatal(err)
			}

			cfg, _, err := Resolve(FlagOverrides{})
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.CollapseLevels || !cfg.HomeWorkDateOnly || !cfg.MergeSameLocationDays {
				t.Errorf("bools must keep their defaults when output-path was never set, got %+v", cfg)
			}
		}},
		{"FileBoolsApplyOnceConfigured", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if err := SaveGlobal(Global{
				OutputPath:     "/tmp/from-file",
				CollapseLevels: false,
			}); err != nil {
				t.Fatal(err)
			}

			cfg, _, err := Resolve(FlagOverrides{})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.CollapseLevels {
				t.Error("explicit collapse-levels: false in a configured file must be honoured")
			}
		}},
		{"FlagBoolOverridesFile", func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if err := SaveGlobal(Global{OutputPath: "/tmp/from-file", CollapseLevels: false}); err != nil {
				t.Fatal(err)
			}

			cfg, _, err := Resolve(FlagOverrides{CollapseLevels: boolPtr(true)})
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
