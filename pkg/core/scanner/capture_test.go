package scanner

import "testing"

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
