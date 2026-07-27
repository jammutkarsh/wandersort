// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package exif

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// seedHashed puts one live file in the state the hash phase leaves behind: a
// registry row with a metadata row holding only its hash. The insert trigger
// is what flips scan_status to HASHED
func seedHashed(t *testing.T, d *db.DB, id int64, dir, name string) {
	t.Helper()
	dbtest.SeedFile(t, d, id, dir, name, 13)
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO file_metadata (file_hash, file_id) VALUES (?, ?)`,
		"hash-of-"+name, id); err != nil {
		t.Fatal(err)
	}
}

func status(t *testing.T, d *db.DB, id int64) string {
	t.Helper()
	var s string
	if err := d.SQL.Get(&s, `SELECT scan_status FROM file_registry WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExtractor(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// A file exiftool cannot read is still a usable file: the pipeline knows
		// its hash and its folder, so the phase persists empty metadata, marks it
		// ANALYZED, and never fails the session
		{"RunToleratesExtractionFailure", func(t *testing.T) {
			ctx := context.Background()
			d := dbtest.New(t)

			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "photo.jpg"), []byte("bytes"), 0o644); err != nil {
				t.Fatal(err)
			}
			seedHashed(t, d, 1, root, "photo.jpg")

			// A nonexistent exiftool path makes every extraction fail
			e := New(d, logger.NewNoopLogger(), filepath.Join(t.TempDir(), "missing-exiftool"), 1)
			count, err := e.Run(ctx)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if count != 1 {
				t.Fatalf("Run extracted %d files, want 1", count)
			}
			d.Writer.Flush()

			if got := status(t, d, 1); got != db.StatusAnalyzed {
				t.Errorf("scan_status = %s, want %s", got, db.StatusAnalyzed)
			}
			var rows int
			if err := d.SQL.Get(&rows,
				`SELECT count(*) FROM file_metadata WHERE file_id = 1 AND exif_make IS NULL`); err != nil {
				t.Fatal(err)
			}
			if rows != 1 {
				t.Errorf("failed extraction should leave the exif columns NULL on the one existing row, got %d such rows", rows)
			}
		}},
		// Sidecars (.AAE) carry no EXIF of their own, so running exiftool on them
		// is wasted work — they are never claimed and stay HASHED
		{"RunSkipsSidecars", func(t *testing.T) {
			ctx := context.Background()
			d := dbtest.New(t)

			seedHashed(t, d, 1, t.TempDir(), "IMG_0001.AAE")
			if _, err := d.ExecContext(ctx,
				`UPDATE file_registry SET media_type = ? WHERE id = 1`, classifier.MediaTypeSidecar); err != nil {
				t.Fatal(err)
			}

			e := New(d, logger.NewNoopLogger(), filepath.Join(t.TempDir(), "missing-exiftool"), 1)
			count, err := e.Run(ctx)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if count != 0 {
				t.Fatalf("Run extracted %d sidecars, want 0", count)
			}
			d.Writer.Flush()

			if got := status(t, d, 1); got != db.StatusHashed {
				t.Errorf("sidecar scan_status = %s, want %s", got, db.StatusHashed)
			}
		}},
		// The write itself: extracted values land on the row the hash phase
		// inserted, without creating a second one
		{"StoreFillsInTheHashedRow", func(t *testing.T) {
			d := dbtest.New(t)

			seedHashed(t, d, 1, t.TempDir(), "photo.jpg")

			e := New(d, logger.NewNoopLogger(), "exiftool", 1)
			if !d.Writer.Write(e.store(1, classifier.CommonMetadata{
				ImageWidth:       "4032",
				ImageHeight:      "3024",
				Orientation:      "6",
				GPSLatitude:      "22.7196",
				GPSLongitude:     "75.8577",
				Make:             "Apple",
				Model:            "iPhone 13",
				DateTimeOriginal: "2024:03:05 11:22:33",
			})) {
				t.Fatal("bulk writer refused the metadata write")
			}
			d.Writer.Flush()

			var row struct {
				Hash        string   `db:"file_hash"`
				Width       *int     `db:"exif_image_width"`
				Lat         *float64 `db:"exif_gps_latitude"`
				Model       *string  `db:"exif_model"`
				Original    *string  `db:"exif_date_time_original"`
				CreateDate  *string  `db:"exif_create_date"`
				Orientation *int     `db:"exif_orientation"`
			}
			if err := d.SQL.Get(&row, `
				SELECT file_hash, exif_image_width, exif_gps_latitude, exif_model,
					exif_date_time_original, exif_create_date, exif_orientation
				FROM file_metadata WHERE file_id = 1`); err != nil {
				t.Fatal(err)
			}
			if row.Hash != "hash-of-photo.jpg" {
				t.Errorf("file_hash = %s, want the hash phase's value untouched", row.Hash)
			}
			if row.Width == nil || *row.Width != 4032 {
				t.Errorf("exif_image_width = %v, want 4032", row.Width)
			}
			if row.Orientation == nil || *row.Orientation != 6 {
				t.Errorf("exif_orientation = %v, want 6", row.Orientation)
			}
			if row.Lat == nil || *row.Lat != 22.7196 {
				t.Errorf("exif_gps_latitude = %v, want 22.7196", row.Lat)
			}
			if row.Model == nil || *row.Model != "iPhone 13" {
				t.Errorf("exif_model = %v, want iPhone 13", row.Model)
			}
			if row.Original == nil || *row.Original != "2024:03:05 11:22:33" {
				t.Errorf("exif_date_time_original = %v", row.Original)
			}
			// absent tags must stay NULL, not become ""
			if row.CreateDate != nil {
				t.Errorf("exif_create_date = %v, want NULL for an absent tag", *row.CreateDate)
			}
			if got := status(t, d, 1); got != db.StatusAnalyzed {
				t.Errorf("scan_status = %s, want %s", got, db.StatusAnalyzed)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}
