package classifier

import (
	"testing"
)

// ---------------------------------------------------------------------------
// ClassifyName
// ---------------------------------------------------------------------------

func TestClassifyName(t *testing.T) {
	fc := NewFileClassifier()

	tests := []struct {
		path     string
		wantType string
	}{
		// Images
		{"photo.jpg", MediaTypeImage},
		{"photo.JPEG", MediaTypeImage},
		{"photo.png", MediaTypeImage},
		{"photo.bmp", MediaTypeImage},
		{"photo.heic", MediaTypeImage},
		{"photo.HEIC", MediaTypeImage},
		{"photo.webp", MediaTypeImage},

		// Videos
		{"video.mp4", MediaTypeVideo},
		{"video.MP4", MediaTypeVideo},
		{"video.mov", MediaTypeVideo},
		{"video.MOV", MediaTypeVideo},

		// RAW
		{"raw.cr2", MediaTypeRaw},
		{"raw.CR2", MediaTypeRaw},
		{"raw.dng", MediaTypeRaw},
		{"raw.DNG", MediaTypeRaw},

		// Sidecar
		{"sidecar.aae", MediaTypeSidecar},
		{"sidecar.AAE", MediaTypeSidecar},

		// Ignored
		{".DS_Store", MediaTypeIgnored},
		{"Thumbs.db", MediaTypeIgnored},
		{"/Volumes/Backups/Pictures/.DS_Store", MediaTypeIgnored},

		// AppleDouble sidecars: they carry the shadowed file's extension, so
		// every one of these would classify as real media without the prefix rule
		{"._IMG_20180106_211920.jpg", MediaTypeIgnored},
		{"._photo.HEIC", MediaTypeIgnored},
		{"._raw.cr2", MediaTypeIgnored},
		{"._clip.mov", MediaTypeIgnored},
		{"._sidecar.aae", MediaTypeIgnored},
		{"/Volumes/Backups/Family/._IMG_0001.jpg", MediaTypeIgnored},
		{"._", MediaTypeIgnored},
		// a leading dot alone is not AppleDouble, and a "._" anywhere but the
		// start is an ordinary filename
		{".hidden.jpg", MediaTypeImage},
		{"my._photo.jpg", MediaTypeImage},

		// Unsupported
		{"readme.txt", MediaTypeUnknown},
		{"script.py", MediaTypeUnknown},
		{"Makefile", MediaTypeUnknown},
		{"archive.zip", MediaTypeUnknown},
		{"", MediaTypeUnknown},

		// Path with directory components
		{"/home/user/Photos/2023/IMG_001.jpg", MediaTypeImage},
		{"~/Pictures/vacation.HEIC", MediaTypeImage},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := fc.ClassifyName(tt.path); got != tt.wantType {
				t.Errorf("ClassifyName(%q) = %q, want %q", tt.path, got, tt.wantType)
			}
		})
	}
}

func TestShouldIgnoreDir(t *testing.T) {
	fc := NewFileClassifier()

	tests := []struct {
		dir  string
		want bool
	}{
		{".git", true},
		{".svn", true},

		// another app's library: its originals must never be moved out
		{"Photos Library.photoslibrary", true},
		{"Photos Library.PhotosLibrary", true},
		{"iPhoto Library.photolibrary", true},
		{"iPhoto Library.migratedphotolibrary", true},
		{"Aperture Library.aplibrary", true},
		{"Catalog Previews.lrdata", true},
		{"Catalog Smart Previews.lrdata", true},
		{"Lightroom Library.lrlibrary", true},
		{"Photos.cocatalogdb", true},
		{"photoslibrary", false}, // a plain folder that happens to be named so
		{"Goa Trip.photos", false},
		{"node_modules", true},
		{".Trash", true},
		{"$RECYCLE.BIN", true},
		{"System Volume Information", true},

		// macOS volume metadata — these hold the index shards that produced
		// almost every "Unsupported file type" warning on the 107k-file run
		{".Spotlight-V100", true},
		{".fseventsd", true},
		{".DocumentRevisions-V100", true},
		{".TemporaryItems", true},
		{".Trashes", true},

		// real directories a library actually uses
		{"DCIM", false},
		{"Camera", false},
		{"WhatsApp Images", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			if got := fc.ShouldIgnoreDir(tt.dir); got != tt.want {
				t.Errorf("ShouldIgnoreDir(%q) = %v, want %v", tt.dir, got, tt.want)
			}
		})
	}
}

func TestIsGenericDirName(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		want bool
	}{
		{"dcim", "dcim", true},
		{"DCIM uppercase", "DCIM", true},
		{"backup", "backup", true},
		{"downloads", "Downloads", true},
		{"desktop", "Desktop", true},
		{"misc", "misc", true},
		{"temp", "temp", true},
		{"photos", "photos", true},
		{"camera", "camera", true},
		{"sync", "sync", true},
		{"cache", "cache", true},
		{"WhatsApp Images", "WhatsApp Images", true},
		{"Telegram media", "Telegram Images", true},
		{"Signal videos", "Signal Media", true},
		{"New Folder", "New Folder", true},
		{"new folder (2)", "New Folder (2)", true},
		{"new folder with spaces", "New Folder (5)", true},
		{"old backup", "old backup", true},
		{"old backup num", "old backup 3", true},
		{"backup 2023", "backup_2023", true},
		{"dcim variant", "DCIM 1", true},
		{"temp variant", "tmp_123", true},
		{".thumbnails", ".thumbnails", true},
		{"trashed", "Trashed documents", true},
		{"trips", "trips", false},
		{"goa", "goa", false},
		{"year", "2024", false},
		{"wedding", "wedding", false},
		{"family", "family", false},
		{"empty name", "", false},
		{"root", "/", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsGenericDirName(tt.dir)
			if got != tt.want {
				t.Errorf("IsGenericDirName(%q) = %v, want %v", tt.dir, got, tt.want)
			}
		})
	}
}
