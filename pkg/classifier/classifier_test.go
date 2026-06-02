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
		path          string
		wantType      string
		wantProcessed bool
		wantIgnored   bool
	}{
		// Images
		{"photo.jpg", MediaTypeImage, true, false},
		{"photo.JPEG", MediaTypeImage, true, false},
		{"photo.png", MediaTypeImage, true, false},
		{"photo.bmp", MediaTypeImage, true, false},
		{"photo.heic", MediaTypeImage, true, false},
		{"photo.HEIC", MediaTypeImage, true, false},
		{"photo.webp", MediaTypeImage, true, false},

		// Videos
		{"video.mp4", MediaTypeVideo, true, false},
		{"video.MP4", MediaTypeVideo, true, false},
		{"video.mov", MediaTypeVideo, true, false},
		{"video.MOV", MediaTypeVideo, true, false},

		// RAW
		{"raw.cr2", MediaTypeRaw, true, false},
		{"raw.CR2", MediaTypeRaw, true, false},
		{"raw.dng", MediaTypeRaw, true, false},
		{"raw.DNG", MediaTypeRaw, true, false},

		// Sidecar
		{"sidecar.aae", MediaTypeSidecar, true, false},
		{"sidecar.AAE", MediaTypeSidecar, true, false},

		// Ignored
		{".DS_Store", MediaTypeUnknown, false, true},
		{"Thumbs.db", MediaTypeUnknown, false, true},

		// Unsupported
		{"readme.txt", MediaTypeUnknown, false, false},
		{"script.py", MediaTypeUnknown, false, false},
		{"Makefile", MediaTypeUnknown, false, false},
		{"archive.zip", MediaTypeUnknown, false, false},
		{"", MediaTypeUnknown, false, false},

		// Path with directory components
		{"/home/user/Photos/2023/IMG_001.jpg", MediaTypeImage, true, false},
		{"~/Pictures/vacation.HEIC", MediaTypeImage, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			mediaType, processed, ignored := fc.ClassifyName(tt.path)
			if mediaType != tt.wantType || processed != tt.wantProcessed || ignored != tt.wantIgnored {
				t.Errorf("ClassifyName(%q) = (%q, %v, %v), want (%q, %v, %v)",
					tt.path, mediaType, processed, ignored, tt.wantType, tt.wantProcessed, tt.wantIgnored)
			}
		})
	}
}
