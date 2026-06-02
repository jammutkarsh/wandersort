package classifier

import (
	"encoding/json"
	"testing"
)

func TestParseFromBytes_Bmp(t *testing.T) {
	// TODO: Use a real file instead.
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
