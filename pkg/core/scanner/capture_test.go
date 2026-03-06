package scanner

import (
	"sync"
	"testing"
)

func TestDeriveCapture(t *testing.T) {
	tests := []struct {
		filename  string
		ext       string
		mediaType string
		wantStem  string
		wantRole  string
	}{
		// iPhone live photo bundle
		{"IMG_3162.HEIC", ".heic", "IMAGE", "IMG_3162", CaptureRoleOriginal},
		{"IMG_3162.MOV", ".mov", "VIDEO", "IMG_3162", CaptureRoleLiveVideo},
		{"IMG_3162.AAE", ".aae", "SIDECAR", "IMG_3162", CaptureRoleSidecar},
		{"IMG_E3162.HEIC", ".heic", "IMAGE", "IMG_3162", CaptureRoleEdited},
		{"IMG_E3162.MOV", ".mov", "VIDEO", "IMG_3162", CaptureRoleEditedVideo},
		{"IMG_O3162.AAE", ".aae", "SIDECAR", "IMG_3162", CaptureRoleOriginalSidecar},

		// iPhone normal photo
		{"IMG_5211.HEIC", ".heic", "IMAGE", "IMG_5211", CaptureRoleOriginal},
		{"IMG_5211.AAE", ".aae", "SIDECAR", "IMG_5211", CaptureRoleSidecar},
		{"IMG_E5211.HEIC", ".heic", "IMAGE", "IMG_5211", CaptureRoleEdited},
		{"IMG_O5211.AAE", ".aae", "SIDECAR", "IMG_5211", CaptureRoleOriginalSidecar},

		// DSLR (Canon)
		{"_MG_1721.JPG", ".jpg", "IMAGE", "_MG_1721", CaptureRoleOriginal},
		{"_MG_1721.CR2", ".cr2", "RAW", "_MG_1721", CaptureRoleRaw},

		// DNG raw
		{"photo.DNG", ".dng", "RAW", "photo", CaptureRoleRaw},

		// MP4 video (edited variant)
		{"IMG_E1001.MP4", ".mp4", "VIDEO", "IMG_1001", CaptureRoleEditedVideo},

		// Plain filename without variant prefix
		{"sunset.jpg", ".jpg", "IMAGE", "sunset", CaptureRoleOriginal},
		{"clip.mp4", ".mp4", "VIDEO", "clip", CaptureRoleLiveVideo},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := DeriveCapture(tt.filename, tt.ext, tt.mediaType)
			if got.Stem != tt.wantStem {
				t.Errorf("Stem = %q, want %q", got.Stem, tt.wantStem)
			}
			if got.Role != tt.wantRole {
				t.Errorf("Role = %q, want %q", got.Role, tt.wantRole)
			}
		})
	}
}

// TestDeriveCaptureConcurrent ensures DeriveCapture is goroutine-safe.
func TestDeriveCaptureConcurrent(t *testing.T) {
	inputs := []struct {
		filename  string
		ext       string
		mediaType string
		wantStem  string
		wantRole  string
	}{
		{"IMG_3162.HEIC", ".heic", "IMAGE", "IMG_3162", CaptureRoleOriginal},
		{"IMG_E3162.HEIC", ".heic", "IMAGE", "IMG_3162", CaptureRoleEdited},
		{"IMG_3162.MOV", ".mov", "VIDEO", "IMG_3162", CaptureRoleLiveVideo},
		{"_MG_1721.CR2", ".cr2", "RAW", "_MG_1721", CaptureRoleRaw},
	}

	var wg sync.WaitGroup
	const goroutines = 50
	errs := make(chan string, goroutines*len(inputs))

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, in := range inputs {
				got := DeriveCapture(in.filename, in.ext, in.mediaType)
				if got.Stem != in.wantStem || got.Role != in.wantRole {
					errs <- in.filename + ": unexpected result"
				}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for e := range errs {
		t.Error(e)
	}
}

// TestDeriveCaptureGrouping verifies files from the same capture event share the same stem.
func TestDeriveCaptureGrouping(t *testing.T) {
	// All of these are part of the same capture group (IMG_3162)
	group := []struct {
		filename  string
		ext       string
		mediaType string
	}{
		{"IMG_3162.HEIC", ".heic", "IMAGE"},
		{"IMG_3162.MOV", ".mov", "VIDEO"},
		{"IMG_3162.AAE", ".aae", "SIDECAR"},
		{"IMG_E3162.HEIC", ".heic", "IMAGE"},
		{"IMG_E3162.MOV", ".mov", "VIDEO"},
		{"IMG_O3162.AAE", ".aae", "SIDECAR"},
	}

	stems := make(map[string]bool)
	for _, f := range group {
		info := DeriveCapture(f.filename, f.ext, f.mediaType)
		stems[info.Stem] = true
	}

	if len(stems) != 1 {
		t.Errorf("Expected all files to share one capture stem, got %d: %v", len(stems), stems)
	}
	if !stems["IMG_3162"] {
		t.Errorf("Expected stem IMG_3162, got %v", stems)
	}
}
