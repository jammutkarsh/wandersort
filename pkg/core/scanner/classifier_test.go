package scanner

import "testing"

func TestClassify(t *testing.T) {
	fc := NewFileClassifier()

	tests := []struct {
		path          string
		wantType      string
		wantProcessed bool
	}{
		{"photo.jpg", MediaTypeImage, true},
		{"photo.JPEG", MediaTypeImage, true},
		{"video.mp4", MediaTypeVideo, true},
		{"video.MKV", MediaTypeVideo, true},
		{"raw.cr2", MediaTypeRaw, true},
		{"sidecar.aae", MediaTypeSidecar, true},
		{"readme.txt", MediaTypeUnknown, false},
		{"script.py", MediaTypeUnknown, false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			mediaType, ok := fc.Classify(tt.path)
			if mediaType != tt.wantType || ok != tt.wantProcessed {
				t.Errorf("Classify(%q) = (%q, %v), want (%q, %v)", tt.path, mediaType, ok, tt.wantType, tt.wantProcessed)
			}
		})
	}
}

func TestShouldIgnore(t *testing.T) {
	fc := NewFileClassifier()

	ignored := []string{".DS_Store", "Thumbs.db", "desktop.ini", ".picasa.ini", ".nomedia"}
	for _, name := range ignored {
		if !fc.ShouldIgnore(name) {
			t.Errorf("ShouldIgnore(%q) = false, want true", name)
		}
	}

	notIgnored := []string{"photo.jpg", "README.md", "file.txt"}
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

	notIgnored := []string{"Photos", "Documents", "src"}
	for _, name := range notIgnored {
		if fc.ShouldIgnoreDir(name) {
			t.Errorf("ShouldIgnoreDir(%q) = true, want false", name)
		}
	}
}

func TestIsPrimarySource(t *testing.T) {
	fc := NewFileClassifier()

	if !fc.IsPrimarySource(MediaTypeImage) {
		t.Error("IsPrimarySource(IMAGE) = false, want true")
	}
	if !fc.IsPrimarySource(MediaTypeRaw) {
		t.Error("IsPrimarySource(RAW) = false, want true")
	}
	if !fc.IsPrimarySource(MediaTypeVideo) {
		t.Error("IsPrimarySource(VIDEO) = false, want true")
	}
	if fc.IsPrimarySource(MediaTypeSidecar) {
		t.Error("IsPrimarySource(SIDECAR) = true, want false")
	}
	if fc.IsPrimarySource(MediaTypeUnknown) {
		t.Error("IsPrimarySource(UNKNOWN) = true, want false")
	}
}
