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
