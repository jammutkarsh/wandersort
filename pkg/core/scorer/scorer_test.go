package scorer

import (
	"context"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
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

func TestIsInGenericDir(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		want bool
	}{
		// Exact matches
		{"dcim root", "dcim", true},
		{"DCIM with subdir", "DCIM/100APPLE", true},
		{"deeply nested in backups", "old/backup/photos", true},
		{"downloads", "Downloads", true},
		{"desktop", "Desktop", true},
		{"misc", "misc/files", true},
		{"temp dir", "/tmp/temp", true},
		{"photos at root", "photos", true},
		{"camera folder", "camera/2024", true},

		// Pattern matches (genericDirPattern)
		{"WhatsApp Images", "WhatsApp Images", true},
		{"Telegram media", "Telegram Images/Sent", true},
		{"Signal videos", "Signal Media", true},
		{"New Folder", "New Folder", true},
		{"new folder (2)", "New Folder (2)", true},
		{"new folder with spaces", "New Folder (5)", true},
		{"old backup", "old backup", true},
		{"old backup num", "old backup 3", true},
		{"backup 2023", "backup_2023", true},
		{"dcim variant", "DCIM 1", true},
		{"temp variant", "tmp_123/abc", true},
		{".thumbnails", ".thumbnails", true},
		{".thumbnails hidden", ".thumbnails/cache", true},
		{"trashed", "Trashed documents", true},
		{"sync folder", "sync/data", true},
		{"cache dir", "cache/thumbnails", true},

		// Good directories
		{"trips", "trips/goa", false},
		{"year month", "2024/05", false},
		{"event name with photos", "wedding/photos", true},
		{"named folder", "family/2023", false},
		{"empty dir", "", false},
		{"root", "/", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsInGenericDir(tt.dir)
			if got != tt.want {
				t.Errorf("IsInGenericDir(%q) = %v, want %v", tt.dir, got, tt.want)
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
	sessionId := dbtest.NewSession(t, d, db.StatusHashed)

	dbtest.SeedFile(t, d, sessionId, 1, "/photos/trips/goa", "sunset.jpg", 1024)
	dbtest.SeedFile(t, d, sessionId, 2, "/backup/dcim", "IMG_3162.jpg", 1024)
	dbtest.SeedFile(t, d, sessionId, 3, "/photos/trips/goa", "beach.jpg", 2048)

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
		n, err := s.Run(ctx, sessionId)
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

	// A soft-deleted duplicate must stop counting as a group member: file 2
	// vanishes, so file 1 becomes a solo master and file 2 keeps its demotion
	if _, err := d.ExecContext(ctx,
		`UPDATE file_registry SET deleted_at = '2026-01-01T00:00:00.000000000Z' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Scorer{db: d, log: logger.NewNoopLogger()}).Run(ctx, sessionId); err != nil {
		t.Fatal(err)
	}
	d.Writer.Flush()
	var master1 bool
	if err := d.SQL.GetContext(ctx, &master1,
		`SELECT is_master FROM file_metadata WHERE file_id = 1`); err != nil {
		t.Fatal(err)
	}
	if !master1 {
		t.Error("survivor of a soft-deleted group was not re-promoted")
	}
	// The soft-deleted member must not be re-promoted alongside the survivor
	var master2 bool
	if err := d.SQL.GetContext(ctx, &master2,
		`SELECT is_master FROM file_metadata WHERE file_id = 2`); err != nil {
		t.Fatal(err)
	}
	if master2 {
		t.Error("soft-deleted member was re-promoted to master")
	}
}

func TestRunDirBonusIgnoresGenericAncestors(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	sessionId := dbtest.NewSession(t, d, db.StatusHashed)

	// Same camera filename, absolute dirs sharing a generic ancestor (Photos).
	// Only the leaf folder may decide the dir bonus: the meaningful leaf must
	// win even though the generic-leaf path is shorter
	dbtest.SeedFile(t, d, sessionId, 1, "/Users/x/Photos/dcim", "IMG_1.jpg", 1024)
	dbtest.SeedFile(t, d, sessionId, 2, "/Users/x/Photos/Goa Trip 2024", "IMG_1.jpg", 1024)
	for _, id := range []int64{1, 2} {
		if _, err := d.ExecContext(ctx,
			`INSERT INTO file_metadata (file_hash, file_id) VALUES ('dup', ?)`, id); err != nil {
			t.Fatal(err)
		}
	}

	s := &Scorer{db: d, log: logger.NewNoopLogger()}
	if _, err := s.Run(ctx, sessionId); err != nil {
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
	sessionId := dbtest.NewSession(t, d, db.StatusHashed)

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
		dbtest.SeedFile(t, d, sessionId, f.id, "/photos/trips/goa", f.name, 1024)
		if _, err := d.ExecContext(ctx,
			`INSERT INTO file_metadata (file_hash, file_id) VALUES ('tied', ?)`, f.id); err != nil {
			t.Fatal(err)
		}
	}

	s := &Scorer{db: d, log: logger.NewNoopLogger()}
	if _, err := s.Run(ctx, sessionId); err != nil {
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
