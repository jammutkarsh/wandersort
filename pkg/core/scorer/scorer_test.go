package scorer

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/db"
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
			got := isInGenericDir(tt.dir)
			if got != tt.want {
				t.Errorf("isInGenericDir(%q) = %v, want %v", tt.dir, got, tt.want)
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

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.New(context.Background(), path, db.AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestRun(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	sessionId := uuid.New()

	_, err := d.ExecContext(ctx, `INSERT INTO scan_sessions (id, status, root_paths) VALUES (?, 'HASHED', '/tmp')`, sessionId.String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.ExecContext(ctx, `INSERT INTO file_registry (id, file_path, file_size, file_modified_at, scan_session_id, source_root, file_extension, media_type)
		VALUES (1, 'trips/goa/sunset.jpg', 1024, '2024-01-01', ?, '/photos', '.jpg', 'IMAGE')`, sessionId.String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.ExecContext(ctx, `INSERT INTO file_registry (id, file_path, file_size, file_modified_at, scan_session_id, source_root, file_extension, media_type)
		VALUES (2, 'dcim/IMG_3162.jpg', 1024, '2024-01-01', ?, '/backup', '.jpg', 'IMAGE')`, sessionId.String())
	if err != nil {
		t.Fatal(err)
	}

	_, err = d.ExecContext(ctx, `INSERT INTO file_metadata (file_hash, file_id) VALUES ('abc', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.ExecContext(ctx, `INSERT INTO file_metadata (file_hash, file_id) VALUES ('abc', 2)`)
	if err != nil {
		t.Fatal(err)
	}

	s := &Scorer{db: d, log: logger.NewNoopLogger()}
	n, err := s.Run(ctx, sessionId)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("Run = %d, want 1", n)
	}
}
