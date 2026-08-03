// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func TestDB(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"NewRefusesForeignDatabase", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "foreign.db")

			// A sqlite file with schema but without our application_id stamp
			foreign, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := foreign.Exec(`CREATE TABLE somebody_elses_data (id INTEGER PRIMARY KEY)`); err != nil {
				t.Fatal(err)
			}
			if err := foreign.Close(); err != nil {
				t.Fatal(err)
			}

			if _, err := New(context.Background(), dbPath, AppDB, logger.NewNoopLogger()); err == nil {
				t.Fatal("New should refuse a non-wandersort database")
			} else if !strings.Contains(err.Error(), "not a wandersort database") {
				t.Fatalf("unexpected error: %v", err)
			}

			// The refused file must be left untouched — no stamp, no migrations
			foreign, err = sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer foreign.Close()
			var appID int32
			if err := foreign.QueryRow("PRAGMA application_id").Scan(&appID); err != nil {
				t.Fatal(err)
			}
			if appID != 0 {
				t.Errorf("refused database was stamped with application_id %d", appID)
			}
		}},
		{"NewFreshAndReopen", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")

			d, err := New(context.Background(), dbPath, AppDB, logger.NewNoopLogger())
			if err != nil {
				t.Fatalf("fresh open: %v", err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}

			// A database we stamped and migrated must reopen cleanly
			d, err = New(context.Background(), dbPath, AppDB, logger.NewNoopLogger())
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			defer d.Close()

			var appID int32
			if err := d.SQL.QueryRow("PRAGMA application_id").Scan(&appID); err != nil {
				t.Fatal(err)
			}
			if appID != appIDFromTag() {
				t.Errorf("application_id = %d, want %d", appID, appIDFromTag())
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

func TestFormatTime(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{"zero value", time.Time{}, "0001-01-01T00:00:00.000000000Z"},
		{
			"non-UTC converts to UTC",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.FixedZone("IST", 5*3600+30*60)),
			"2024-06-15T06:30:00.000000000Z",
		},
		{
			"sub-second precision preserved",
			time.Date(2024, 6, 15, 12, 0, 0, 123456789, time.UTC),
			"2024-06-15T12:00:00.123456789Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatTime(tt.in); got != tt.want {
				t.Errorf("FormatTime(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	// round-trip: formatting then parsing back with TimeLayout must reproduce
	// the same instant, since every stored timestamp goes through this pair
	original := time.Date(2024, 6, 15, 12, 30, 45, 999000000, time.UTC)
	formatted := FormatTime(original)
	parsed, err := time.Parse(TimeLayout, formatted)
	if err != nil {
		t.Fatalf("parsing formatted time %q: %v", formatted, err)
	}
	if !parsed.Equal(original) {
		t.Errorf("round-trip: got %v, want %v", parsed, original)
	}
}

func TestIntOrNil(t *testing.T) {
	tests := []struct {
		in   string
		want any
	}{
		{"", nil},
		{"not-a-number", nil},
		{"42", 42},
		{"-7", -7},
		{"0", 0},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := IntOrNil(tt.in); got != tt.want {
				t.Errorf("IntOrNil(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestFloatOrNil(t *testing.T) {
	tests := []struct {
		in   string
		want any
	}{
		{"", nil},
		{"not-a-float", nil},
		{"3.14", 3.14},
		{"-2.5", -2.5},
		{"0", float64(0)},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := FloatOrNil(tt.in); got != tt.want {
				t.Errorf("FloatOrNil(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestStrOrNil(t *testing.T) {
	tests := []struct {
		in   string
		want any
	}{
		{"", nil},
		{"hello", "hello"},
		{" ", " "},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := StrOrNil(tt.in); got != tt.want {
				t.Errorf("StrOrNil(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestCheckpoint(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	if err := d.Checkpoint(); err != nil {
		t.Errorf("Checkpoint: %v", err)
	}
}

func TestOptimize(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	if err := d.Optimize(context.Background()); err != nil {
		t.Errorf("Optimize: %v", err)
	}
}

func TestQueryContextAndQueryRowContext(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()

	if _, err := d.ExecContext(ctx, `INSERT INTO user_labels (label, kind) VALUES ('a', 'EVENT')`); err != nil {
		t.Fatal(err)
	}

	rows, err := d.QueryContext(ctx, `SELECT label FROM user_labels`)
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			t.Fatal(err)
		}
		got = append(got, label)
	}
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("QueryContext rows = %v, want [a]", got)
	}

	var count int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM user_labels`).Scan(&count); err != nil {
		t.Fatalf("QueryRowContext: %v", err)
	}
	if count != 1 {
		t.Errorf("QueryRowContext count = %d, want 1", count)
	}
}

func TestOpenLocationDB(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := New(context.Background(), filepath.Join(t.TempDir(), "missing.db"), LocationDB, logger.NewNoopLogger())
		if err == nil || !strings.Contains(err.Error(), "location database not found") {
			t.Fatalf("got %v, want a not-found error", err)
		}
	})

	t.Run("opens an existing file read-only, no Writer", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "location.db")
		seed, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := seed.Exec(`CREATE TABLE geonames_cities (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		if err := seed.Close(); err != nil {
			t.Fatal(err)
		}

		d, err := New(context.Background(), dbPath, LocationDB, logger.NewNoopLogger())
		if err != nil {
			t.Fatalf("New(LocationDB): %v", err)
		}
		defer d.SQL.Close()

		if d.Writer != nil {
			t.Error("LocationDB connection must have a nil Writer")
		}
		var name string
		if err := d.SQL.QueryRow(`SELECT name FROM sqlite_master WHERE name='geonames_cities'`).Scan(&name); err != nil {
			t.Fatalf("querying seeded table: %v", err)
		}

		// mode=ro: a write attempt must fail
		if _, err := d.SQL.Exec(`INSERT INTO geonames_cities (id) VALUES (1)`); err == nil {
			t.Error("write to a mode=ro location DB should fail")
		}
	})
}
