package exiftool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
)

func TestExtractFirst_ValidArray(t *testing.T) {
	input := `[{"FileName": "test.jpg", "ImageWidth": 4032}]`
	first, err := extractFirst([]byte(input))
	if err != nil {
		t.Fatalf("extractFirst: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(first, &m); err != nil {
		t.Fatalf("unmarshal first element: %v", err)
	}
	if m["FileName"] != "test.jpg" {
		t.Errorf("FileName = %v, want test.jpg", m["FileName"])
	}
}

func TestExtractFirst_MultiElement(t *testing.T) {
	input := `[{"FileName": "a.jpg"}, {"FileName": "b.jpg"}]`
	first, err := extractFirst([]byte(input))
	if err != nil {
		t.Fatalf("extractFirst: %v", err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(first, &m)
	if m["FileName"] != "a.jpg" {
		t.Errorf("expected first element, got %v", m["FileName"])
	}
}

func TestExtractFirst_EmptyArray(t *testing.T) {
	_, err := extractFirst([]byte(`[]`))
	if err == nil {
		t.Error("expected error for empty array")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty: %v", err)
	}
}

func TestExtractFirst_NotArray(t *testing.T) {
	_, err := extractFirst([]byte(`{"key": "value"}`))
	if err == nil {
		t.Error("expected error for non-array JSON")
	}
}

func TestExtractFirst_MalformedJSON(t *testing.T) {
	_, err := extractFirst([]byte(`not json`))
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestDispatch_AllRegisteredExtensions(t *testing.T) {
	data := []byte(`{}`)
	registered := []string{
		".jpg", ".jpeg", ".heic", ".png", ".bmp",
		".webp", ".cr2", ".dng", ".mp4", ".mov", ".aae",
	}
	for _, ext := range registered {
		t.Run(ext, func(t *testing.T) {
			_, err := dispatch(ext, data)
			if err != nil {
				t.Errorf("dispatch(%q) error: %v", ext, err)
			}
		})
	}
}

func TestDispatch_CaseInsensitive(t *testing.T) {
	data := []byte(`{}`)
	variants := []string{".JPG", ".Jpg", ".jPg", ".HEIC", ".Mp4"}
	for _, ext := range variants {
		t.Run(ext, func(t *testing.T) {
			_, err := dispatch(ext, data)
			if err != nil {
				t.Errorf("dispatch(%q) error: %v", ext, err)
			}
		})
	}
}

func TestDispatch_UnsupportedExtension(t *testing.T) {
	_, err := dispatch(".xyz", []byte(`{}`))
	if err == nil {
		t.Error("expected error for unsupported extension")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error should mention unsupported: %v", err)
	}
}

func TestDispatch_InvalidJSON(t *testing.T) {
	_, err := dispatch(".jpg", []byte(`not valid json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestDispatchRegistry_Completeness(t *testing.T) {
	expectedExts := []string{
		".jpg", ".jpeg", ".heic", ".png", ".bmp", ".webp",
		".cr2", ".dng", ".mp4", ".mov", ".aae",
	}
	for _, ext := range expectedExts {
		if _, ok := dispatchRegistry[ext]; !ok {
			t.Errorf("dispatchRegistry missing extension %q", ext)
		}
	}
	if got := len(dispatchRegistry); got != len(expectedExts) {
		t.Errorf("dispatchRegistry has %d entries, expected %d", got, len(expectedExts))
	}
}

func TestDispatch_JpgRoundTrip(t *testing.T) {
	data := []byte(`{"ImageWidth":4032,"ImageHeight":3024,"Make":"Apple","Model":"iPhone 14 Pro","DateTimeOriginal":"2023:06:15 14:30:00","GPSLatitude":37.7749,"GPSLongitude":-122.4194}`)
	common, err := dispatch(".jpg", data)
	if err != nil {
		t.Fatalf("dispatch(.jpg): %v", err)
	}
	if common.ImageWidth != "4032" {
		t.Errorf("ImageWidth = %q, want 4032", common.ImageWidth)
	}
	if common.Make != "Apple" {
		t.Errorf("Make = %q, want Apple", common.Make)
	}
	if common.DateTimeOriginal != "2023:06:15 14:30:00" {
		t.Errorf("DateTimeOriginal = %q", common.DateTimeOriginal)
	}
}

func TestDispatch_HeicRoundTrip(t *testing.T) {
	data := []byte(`{"ImageWidth":4032,"ImageHeight":3024,"Make":"Apple","Model":"iPhone 15 Pro Max"}`)
	common, err := dispatch(".heic", data)
	if err != nil {
		t.Fatalf("dispatch(.heic): %v", err)
	}
	if common.Model != "iPhone 15 Pro Max" {
		t.Errorf("Model = %q", common.Model)
	}
}

func TestDispatch_Mp4RoundTrip(t *testing.T) {
	data := []byte(`{"ImageWidth":1920,"ImageHeight":1080,"Make":"Apple","Model":"iPhone 14 Pro"}`)
	common, err := dispatch(".mp4", data)
	if err != nil {
		t.Fatalf("dispatch(.mp4): %v", err)
	}
	if common.ImageWidth != "1920" {
		t.Errorf("ImageWidth = %q, want 1920", common.ImageWidth)
	}
	if common.ImageHeight != "1080" {
		t.Errorf("ImageHeight = %q, want 1080", common.ImageHeight)
	}
}

func TestDispatch_AaeReturnsCommon(t *testing.T) {
	data := []byte(`{}`)
	common, err := dispatch(".aae", data)
	if err != nil {
		t.Fatalf("dispatch(.aae): %v", err)
	}
	if common.ImageWidth != "" {
		t.Errorf("AAE ImageWidth should be empty, got %q", common.ImageWidth)
	}
}

func TestConfig_SetDefaults(t *testing.T) {
	c := Config{}
	c.setDefaults()
	if c.Workers != 4 {
		t.Errorf("default Workers = %d, want 4", c.Workers)
	}
	c2 := Config{Workers: 8}
	c2.setDefaults()
	if c2.Workers != 8 {
		t.Errorf("explicit Workers = %d, want 8", c2.Workers)
	}
	c3 := Config{Workers: -1}
	c3.setDefaults()
	if c3.Workers != 4 {
		t.Errorf("negative Workers should default to 4, got %d", c3.Workers)
	}
}

func TestNewExtractor(t *testing.T) {
	e := New(Config{Workers: 2})
	if e.cfg.Workers != 2 {
		t.Errorf("Workers = %d, want 2", e.cfg.Workers)
	}
}

func TestResult_Fields(t *testing.T) {
	r := Result{
		SourceFile: "/path/to/file.jpg",
		Common:     classifier.CommonMetadata{ImageWidth: "4032"},
		Err:        nil,
	}
	if r.SourceFile != "/path/to/file.jpg" {
		t.Error("SourceFile mismatch")
	}
	if r.Common.ImageWidth != "4032" {
		t.Error("Common.ImageWidth mismatch")
	}
	if r.Err != nil {
		t.Error("Err should be nil")
	}
}

func TestDispatch_ConcurrentSafety(t *testing.T) {
	data := []byte(`{"ImageWidth":100,"ImageHeight":200}`)
	exts := []string{".jpg", ".jpeg", ".heic", ".png", ".bmp", ".webp", ".cr2", ".dng", ".mp4", ".mov", ".aae"}
	const goroutines = 50
	results := make(chan error, goroutines)
	for i := range goroutines {
		ext := exts[i%len(exts)]
		go func(e string) {
			_, err := dispatch(e, data)
			results <- err
		}(ext)
	}
	for range goroutines {
		if err := <-results; err != nil {
			t.Errorf("concurrent dispatch error: %v", err)
		}
	}
}
