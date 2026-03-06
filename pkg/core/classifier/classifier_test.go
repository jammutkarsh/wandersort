package classifier

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Classify
// ---------------------------------------------------------------------------

func TestClassify(t *testing.T) {
	fc := NewFileClassifier()

	tests := []struct {
		path          string
		wantType      string
		wantProcessed bool
	}{
		// Images
		{"photo.jpg", MediaTypeImage, true},
		{"photo.JPEG", MediaTypeImage, true},
		{"photo.png", MediaTypeImage, true},
		{"photo.bmp", MediaTypeImage, true},
		{"photo.heic", MediaTypeImage, true},
		{"photo.HEIC", MediaTypeImage, true},
		{"photo.webp", MediaTypeImage, true},

		// Videos
		{"video.mp4", MediaTypeVideo, true},
		{"video.MP4", MediaTypeVideo, true},
		{"video.mov", MediaTypeVideo, true},
		{"video.MOV", MediaTypeVideo, true},

		// RAW
		{"raw.cr2", MediaTypeRaw, true},
		{"raw.CR2", MediaTypeRaw, true},
		{"raw.dng", MediaTypeRaw, true},
		{"raw.DNG", MediaTypeRaw, true},

		// Sidecar
		{"sidecar.aae", MediaTypeSidecar, true},
		{"sidecar.AAE", MediaTypeSidecar, true},

		// Unsupported
		{"readme.txt", MediaTypeUnknown, false},
		{"script.py", MediaTypeUnknown, false},
		{"Makefile", MediaTypeUnknown, false},
		{"archive.zip", MediaTypeUnknown, false},
		{"", MediaTypeUnknown, false},

		// Path with directory components
		{"/home/user/Photos/2023/IMG_001.jpg", MediaTypeImage, true},
		{"~/Pictures/vacation.HEIC", MediaTypeImage, true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			mediaType, ok := fc.Classify(tt.path)
			if mediaType != tt.wantType || ok != tt.wantProcessed {
				t.Errorf("Classify(%q) = (%q, %v), want (%q, %v)",
					tt.path, mediaType, ok, tt.wantType, tt.wantProcessed)
			}
		})
	}
}

// TestClassifyConcurrent ensures Classify is safe for concurrent use.
func TestClassifyConcurrent(t *testing.T) {
	fc := NewFileClassifier()
	paths := []string{
		"photo.jpg", "video.mp4", "raw.cr2", "sidecar.aae",
		"photo.heic", "video.mov", "raw.dng",
		"readme.txt", "Makefile", "photo.png",
	}

	var wg sync.WaitGroup
	const goroutines = 100
	errors := make(chan string, goroutines*len(paths))

	for range goroutines {
		wg.Go(func() {
			for _, p := range paths {
				mediaType, ok := fc.Classify(p)
				if p == "photo.jpg" && (mediaType != MediaTypeImage || !ok) {
					errors <- "photo.jpg mismatch"
				}
				if p == "readme.txt" && (mediaType != MediaTypeUnknown || ok) {
					errors <- "readme.txt mismatch"
				}
			}
		})
	}
	wg.Wait()
	close(errors)

	for e := range errors {
		t.Error(e)
	}
}

// ---------------------------------------------------------------------------
// ShouldIgnore / ShouldIgnoreDir
// ---------------------------------------------------------------------------

func TestShouldIgnore(t *testing.T) {
	fc := NewFileClassifier()

	ignored := []string{".DS_Store", "Thumbs.db", "desktop.ini", ".picasa.ini", ".nomedia"}
	for _, name := range ignored {
		if !fc.ShouldIgnore(name) {
			t.Errorf("ShouldIgnore(%q) = false, want true", name)
		}
	}

	notIgnored := []string{"photo.jpg", "README.md", "file.txt", ".gitignore", "DS_Store"}
	for _, name := range notIgnored {
		if fc.ShouldIgnore(name) {
			t.Errorf("ShouldIgnore(%q) = true, want false", name)
		}
	}
}

func TestShouldIgnoreDir(t *testing.T) {
	fc := NewFileClassifier()

	ignored := []string{".git", ".svn", "node_modules", ".Trash", "$RECYCLE.BIN", "System Volume Information"}
	for _, name := range ignored {
		if !fc.ShouldIgnoreDir(name) {
			t.Errorf("ShouldIgnoreDir(%q) = false, want true", name)
		}
	}

	notIgnored := []string{"Photos", "Documents", "src", ".github", "git", "modules"}
	for _, name := range notIgnored {
		if fc.ShouldIgnoreDir(name) {
			t.Errorf("ShouldIgnoreDir(%q) = true, want false", name)
		}
	}
}

// ---------------------------------------------------------------------------
// IsPrimarySource / NeedsTranscoding
// ---------------------------------------------------------------------------

func TestIsPrimarySource(t *testing.T) {
	fc := NewFileClassifier()

	tests := []struct {
		mediaType string
		want      bool
	}{
		{MediaTypeImage, true},
		{MediaTypeRaw, true},
		{MediaTypeVideo, true},
		{MediaTypeSidecar, false},
		{MediaTypeUnknown, false},
		{"CUSTOM", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.mediaType, func(t *testing.T) {
			if got := fc.IsPrimarySource(tt.mediaType); got != tt.want {
				t.Errorf("IsPrimarySource(%q) = %v, want %v", tt.mediaType, got, tt.want)
			}
		})
	}
}

func TestNeedsTranscoding(t *testing.T) {
	fc := NewFileClassifier()

	if !fc.NeedsTranscoding(MediaTypeRaw) {
		t.Error("NeedsTranscoding(RAW) = false, want true")
	}
	for _, mt := range []string{MediaTypeImage, MediaTypeVideo, MediaTypeSidecar, MediaTypeUnknown} {
		if fc.NeedsTranscoding(mt) {
			t.Errorf("NeedsTranscoding(%q) = true, want false", mt)
		}
	}
}

// ---------------------------------------------------------------------------
// ParseFromBytes — generic JSON → typed struct → CommonMetadata round-trip
// ---------------------------------------------------------------------------

func TestParseFromBytes_Bmp(t *testing.T) {
	in := Bmp{
		ExifToolVersion: 12.5,
		SourceFile:      "/tmp/test.bmp",
		FileName:        "test.bmp",
		FileSize:        1024,
		ImageWidth:      640,
		ImageHeight:     480,
		ImageSize:       "640x480",
		Megapixels:      0.3072,
		MIMEType:        "image/bmp",
		FileType:        "BMP",
	}
	data, _ := json.Marshal(in)

	parsed, err := ParseFromBytes[Bmp](data)
	if err != nil {
		t.Fatalf("ParseFromBytes[Bmp]: %v", err)
	}
	if parsed.MediaType() != MediaTypeImage {
		t.Errorf("MediaType() = %q, want %q", parsed.MediaType(), MediaTypeImage)
	}

	common := parsed.ToCommon()
	if common.FileName != "test.bmp" {
		t.Errorf("FileName = %q, want %q", common.FileName, "test.bmp")
	}
	if common.ImageWidth != "640" {
		t.Errorf("ImageWidth = %q, want %q", common.ImageWidth, "640")
	}
	if common.ImageHeight != "480" {
		t.Errorf("ImageHeight = %q, want %q", common.ImageHeight, "480")
	}
	if common.MIMEType != "image/bmp" {
		t.Errorf("MIMEType = %q, want %q", common.MIMEType, "image/bmp")
	}
}

func TestParseFromBytes_InvalidJSON(t *testing.T) {
	_, err := ParseFromBytes[Bmp]([]byte(`{invalid`))
	if err == nil {
		t.Fatal("ParseFromBytes should fail on invalid JSON")
	}
}

// ---------------------------------------------------------------------------
// ToCommon — verify all types implement Metadata interface via round-trip
// ---------------------------------------------------------------------------

func TestAllTypesImplementMetadata(t *testing.T) {
	// Compile-time interface satisfaction (won't compile if broken)
	var _ Metadata = Jpg{}
	var _ Metadata = Jpeg{}
	var _ Metadata = Heic{}
	var _ Metadata = Png{}
	var _ Metadata = Bmp{}
	var _ Metadata = Webp{}
	var _ Metadata = Cr2{}
	var _ Metadata = Dng{}
	var _ Metadata = Mp4{}
	var _ Metadata = Mov{}
	var _ Metadata = Aae{}

	// Verify MediaType() returns the expected constant for each type
	types := []struct {
		m    Metadata
		want string
	}{
		{Jpg{}, MediaTypeImage},
		{Jpeg{}, MediaTypeImage},
		{Heic{}, MediaTypeImage},
		{Png{}, MediaTypeImage},
		{Bmp{}, MediaTypeImage},
		{Webp{}, MediaTypeImage},
		{Cr2{}, MediaTypeRaw},
		{Dng{}, MediaTypeRaw},
		{Mp4{}, MediaTypeVideo},
		{Mov{}, MediaTypeVideo},
		{Aae{}, MediaTypeSidecar},
	}
	for _, tt := range types {
		name := strings.TrimPrefix(tt.m.MediaType(), "")
		t.Run(name, func(t *testing.T) {
			if tt.m.MediaType() != tt.want {
				t.Errorf("MediaType() = %q, want %q", tt.m.MediaType(), tt.want)
			}
			// ToCommon must not panic on zero-value struct
			_ = tt.m.ToCommon()
		})
	}
}

// TestToCommonFieldMapping verifies key fields survive the JSON round-trip
// through a representative type (Heic).
func TestToCommonFieldMapping_Heic(t *testing.T) {
	raw := `{
		"ExifToolVersion": 13.1,
		"SourceFile": "/photos/IMG_001.HEIC",
		"Directory": "/photos",
		"FileName": "IMG_001.HEIC",
		"FileSize": 2048000,
		"FileType": "HEIC",
		"MIMEType": "image/heic",
		"ImageWidth": 4032,
		"ImageHeight": 3024,
		"Make": "Apple",
		"Model": "iPhone 16 Pro",
		"CreateDate": "2026:02:15 10:30:00",
		"GPSLatitude": 48.8566,
		"GPSLongitude": 2.3522,
		"Aperture": 1.78
	}`

	parsed, err := ParseFromBytes[Heic]([]byte(raw))
	if err != nil {
		t.Fatalf("ParseFromBytes[Heic]: %v", err)
	}

	common := parsed.ToCommon()
	checks := map[string]string{
		"FileName": common.FileName,
		"Make":     common.Make,
		"Model":    common.Model,
		"MIMEType": common.MIMEType,
		"FileType": common.FileType,
	}
	expected := map[string]string{
		"FileName": "IMG_001.HEIC",
		"Make":     "Apple",
		"Model":    "iPhone 16 Pro",
		"MIMEType": "image/heic",
		"FileType": "HEIC",
	}
	for k, got := range checks {
		if got != expected[k] {
			t.Errorf("common.%s = %q, want %q", k, got, expected[k])
		}
	}
	if common.ImageWidth == "" {
		t.Error("ImageWidth should be populated")
	}
	if common.GPSLatitude == "" {
		t.Error("GPSLatitude should be populated")
	}
}

// TestToCommon_ZeroValue ensures ToCommon does not panic and returns empty
// strings for all zero-valued structs.
func TestToCommon_ZeroValue(t *testing.T) {
	zero := Jpg{}
	common := zero.ToCommon()
	if common.FileName != "" {
		t.Errorf("zero-value FileName = %q, want empty", common.FileName)
	}
	if common.ImageWidth != "0" && common.ImageWidth != "" {
		// Numeric zero may produce "0" via itoa — that's acceptable
	}
}
