// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"context"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func TestIsMeaningfulName(t *testing.T) {
	tests := []struct {
		name string
		stem string
		want bool
	}{
		// Human-named
		{"plain word", "sunset", true},
		{"with hyphen", "wedding-ceremony", true},
		{"with underscore", "Goa_trip", true},
		{"mixed case", "Bali2024", true},
		{"date then word", "20230520_wedding", true},

		// Camera patterns (from DCF spec + known manufacturers)
		{"iPhone", "IMG_3162", false},
		{"Canon image", "IMG_0001", false},
		{"Sony/Nikon", "DSC_1234", false},
		{"Sony/Nikon no underscore", "DSC01234", false},
		{"Nikon Coolpix", "DSCN0001", false},
		{"Fujifilm", "DSCF5678", false},
		{"Canon Adobe RGB", "_MG_1721", false},
		{"Sony Adobe RGB", "_DSC1234", false},
		{"Sony Adobe RGB underscore", "_DSC_9999", false},
		{"Google Pixel", "PXL_20230520", false},
		{"Canon video", "MVI_0001", false},
		{"Samsung", "SAM_0001", false},
		{"Panorama", "PANO_0001", false},
		{"Generic video", "VID_0001", false},
		{"Windows Phone", "WP_0001", false},
		{"Canon burst", "CSI_0001", false},
		{"Ricoh", "RIMG0001", false},
		{"Casio", "CIMG0001", false},
		{"Panasonic", "P1000001", false},
		{"GoPro Hero", "GOPR1234", false},
		{"GoPro Hero 8+", "GH0100001", false},
		{"DCF generic", "ABCD0001", false},

		// Insta360 multi-segment naming
		{"Insta360 image", "IMG_20240501_143000_00_001", false},
		{"Insta360 video", "VID_20240501_143000_00_005", false},

		// Not meaningful (no letters)
		{"pure digits", "20230520_143000", false},
		{"numeric only", "12345", false},

		// Edge cases
		{"empty", "", false},
		{"single letter", "a", true},
		{"underscores only", "___", false},
		{"DCIM prefix with digits", "DCIM_001", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := !cameraPattern.MatchString(tt.stem) && hasLetterPattern.MatchString(tt.stem)
			if got != tt.want {
				t.Errorf("isMeaningfulName(%q) = %v, want %v", tt.stem, got, tt.want)
			}
		})
	}
}

func TestDuplicateSuffixPattern(t *testing.T) {
	tests := []struct {
		stem string
		want bool
	}{
		{"IMG_3162 (1)", true},
		{"photo (2)", true},
		{"vacation (42)", true},
		{"sunset - Copy", true},
		{"sunset - copy", true},
		{"document copy", true},
		{"document copy 3", true},
		{"presentation Copy (1)", true},
		{"wedding_photo (1)", true},
		// No match
		{"IMG_3162", false},
		{"sunset", false},
		{"copy-of-file", false}, // "copy" as prefix, not suffix
		{"(1) at start", false}, // not at end
	}

	for _, tt := range tests {
		t.Run(tt.stem, func(t *testing.T) {
			got := duplicateSuffixPattern.MatchString(tt.stem)
			if got != tt.want {
				t.Errorf("duplicateSuffixPattern.MatchString(%q) = %v, want %v", tt.stem, got, tt.want)
			}
		})
	}
}

func TestDatePattern(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"20230520_143000.jpg", true},
		{"2023-05-20_sunset.jpg", true},
		{"2023_05_20_sunset.jpg", true},
		{"20230520_wedding.jpg", true},
		{"IMG_3162.jpg", false},
		{"sunset.jpg", false},
		{"2023-05-20.jpg", false}, // no underscore after date
		{"20230520.jpg", false},   // no underscore after 8 digits
		{"20230520", false},       // not a filename
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := datePattern.MatchString(tt.name)
			if got != tt.want {
				t.Errorf("datePattern.MatchString(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestRun(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()

	dbtest.SeedFile(t, d, 1, "/photos/trips/goa", "sunset.jpg", 1024)
	dbtest.SeedFile(t, d, 2, "/backup/dcim", "IMG_3162.jpg", 1024)
	dbtest.SeedFile(t, d, 3, "/photos/trips/goa", "beach.jpg", 2048)

	for _, seed := range []struct {
		hash   string
		fileID int64
	}{{"abc", 1}, {"abc", 2}, {"solo", 3}} {
		if _, err := d.ExecContext(ctx, `INSERT INTO file_metadata (file_hash, file_id) VALUES (?, ?)`,
			seed.hash, seed.fileID); err != nil {
			t.Fatal(err)
		}
	}

	s := &Scorer{db: d, log: logger.NewNoopLogger()}

	assertMasters := func() {
		t.Helper()
		n, err := s.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("Run = %d, want 1", n)
		}
		d.Writer.Flush()

		masters := map[int64]bool{}
		rows := []struct {
			FileID   int64 `db:"file_id"`
			IsMaster bool  `db:"is_master"`
		}{}
		if err := d.SQL.SelectContext(ctx, &rows, `SELECT file_id, is_master FROM file_metadata`); err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			masters[r.FileID] = r.IsMaster
		}
		// File 1 (meaningful name, non-generic dir) beats file 2 (camera name in DCIM).
		want := map[int64]bool{1: true, 2: false, 3: true}
		for id, wantMaster := range want {
			if masters[id] != wantMaster {
				t.Errorf("file %d is_master = %v, want %v", id, masters[id], wantMaster)
			}
		}
	}

	assertMasters()
	// Re-running is idempotent: same winners, no flapping.
	assertMasters()

	// A demoted file whose duplicate group shrank to one member (the rest
	// swept by a re-scan) must be re-promoted, or it stays invisible to VFS.
	if _, err := d.ExecContext(ctx, `UPDATE file_metadata SET is_master = 0 WHERE file_id = 3`); err != nil {
		t.Fatal(err)
	}
	assertMasters()

	// A vanished duplicate must stop counting as a group member: file 2 is
	// hard-deleted (as a real sweep would delete it, metadata row included),
	// so file 1 becomes a solo master
	if _, err := d.ExecContext(ctx, `DELETE FROM file_metadata WHERE file_id = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `DELETE FROM file_registry WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Scorer{db: d, log: logger.NewNoopLogger()}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	d.Writer.Flush()
	var master1 bool
	if err := d.SQL.GetContext(ctx, &master1,
		`SELECT is_master FROM file_metadata WHERE file_id = 1`); err != nil {
		t.Fatal(err)
	}
	if !master1 {
		t.Error("survivor of a vanished group was not re-promoted")
	}
	var remaining int
	if err := d.SQL.GetContext(ctx, &remaining, `SELECT COUNT(*) FROM file_metadata WHERE file_id = 2`); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Error("vanished member's metadata row survived")
	}
}

// A placed file always keeps the election for its hash, even when a
// re-scanned duplicate out-scores it on path heuristics alone — otherwise
// execute proposes and copies the duplicate again, and the placed file's own
// row gets deleted for having "lost", leaving an orphaned second copy on
// disk with no database record (issue 06).
func TestRunNeverDemotesAPlacedFile(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)

	// file 1: already placed, in the app's own low-scoring default folder
	dbtest.SeedFile(t, d, 1, "/library/2024/06_June/Goa/Photos", "IMG_1234.jpg", 1024)
	if _, err := d.ExecContext(ctx, `UPDATE file_registry SET placed = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	// file 2: a re-scanned duplicate sitting in a meaningfully-named folder,
	// which perFileScore favors over "Photos"
	dbtest.SeedFile(t, d, 2, "/card/Goa Trip", "IMG_1234.jpg", 1024)

	for _, seed := range []struct {
		hash   string
		fileID int64
	}{{"same-hash", 1}, {"same-hash", 2}} {
		if _, err := d.ExecContext(ctx, `INSERT INTO file_metadata (file_hash, file_id) VALUES (?, ?)`,
			seed.hash, seed.fileID); err != nil {
			t.Fatal(err)
		}
	}
	dbtest.SeedEntry(t, d, 1, "2024/06_June/Goa/Photos/IMG_1234.jpg", "2024/06_June/Goa/Photos/IMG_1234.jpg")
	dbtest.SeedPlaced(t, d, 1)

	if _, err := (&Scorer{db: d, log: logger.NewNoopLogger()}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	d.Writer.Flush()

	masters := map[int64]bool{}
	rows := []struct {
		FileID   int64 `db:"file_id"`
		IsMaster bool  `db:"is_master"`
	}{}
	if err := d.SQL.SelectContext(ctx, &rows, `SELECT file_id, is_master FROM file_metadata`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		masters[r.FileID] = r.IsMaster
	}
	if !masters[1] {
		t.Error("placed file lost the election to a higher-scoring re-scanned duplicate")
	}
	if masters[2] {
		t.Error("re-scanned duplicate was elected master over the placed file")
	}
}

func TestRunDirBonusIgnoresGenericAncestors(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()

	// Same camera filename, absolute dirs sharing a generic ancestor (Photos).
	// Only the leaf folder may decide the dir bonus: the meaningful leaf must
	// win even though the generic-leaf path is shorter
	dbtest.SeedFile(t, d, 1, "/Users/x/Photos/dcim", "IMG_1.jpg", 1024)
	dbtest.SeedFile(t, d, 2, "/Users/x/Photos/Goa Trip 2024", "IMG_1.jpg", 1024)
	for _, id := range []int64{1, 2} {
		if _, err := d.ExecContext(ctx,
			`INSERT INTO file_metadata (file_hash, file_id) VALUES ('dup', ?)`, id); err != nil {
			t.Fatal(err)
		}
	}

	s := &Scorer{db: d, log: logger.NewNoopLogger()}
	if _, err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	d.Writer.Flush()

	var masterID int64
	if err := d.SQL.GetContext(ctx, &masterID,
		`SELECT file_id FROM file_metadata WHERE is_master = 1`); err != nil {
		t.Fatal(err)
	}
	if masterID != 2 {
		t.Errorf("master = file %d, want file 2 (meaningful leaf folder must earn dir bonus)", masterID)
	}
}

func TestRunDeterministicTieBreak(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()

	// Two duplicates with identical score and identical path length; the
	// (file_dir, file_name) ordering must decide the winner, not the
	// insertion order — so insert the expected loser first
	seed := []struct {
		id   int64
		name string
	}{
		{1, "beach_b.jpg"},
		{2, "beach_a.jpg"},
	}
	for _, f := range seed {
		dbtest.SeedFile(t, d, f.id, "/photos/trips/goa", f.name, 1024)
		if _, err := d.ExecContext(ctx,
			`INSERT INTO file_metadata (file_hash, file_id) VALUES ('tied', ?)`, f.id); err != nil {
			t.Fatal(err)
		}
	}

	s := &Scorer{db: d, log: logger.NewNoopLogger()}
	if _, err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	d.Writer.Flush()

	var masterID int64
	if err := d.SQL.GetContext(ctx, &masterID,
		`SELECT file_id FROM file_metadata WHERE is_master = 1`); err != nil {
		t.Fatal(err)
	}
	if masterID != 2 {
		t.Errorf("tie-break master = file %d, want file 2 (first by file_name order)", masterID)
	}
}
