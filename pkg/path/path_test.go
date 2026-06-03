package path

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathUtil_ExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home dir: %v", err)
	}

	pu := &Resolver{HomeDir: home}

	tests := []struct {
		input string
		want  string
	}{
		{"~/Photos", filepath.Join(home, "Photos")},
		{"~/Photos/2023/trip", filepath.Join(home, "Photos/2023/trip")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~", "~"}, // only "~/" prefix triggers expansion
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := pu.ExpandPath(tt.input)
			if got != tt.want {
				t.Errorf("ExpandPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPathUtil_ContractPath(t *testing.T) {
	pu := &Resolver{HomeDir: "/home/testuser"}

	tests := []struct {
		input string
		want  string
	}{
		{"/home/testuser/Photos/2023", "~/Photos/2023"},
		{"/home/testuser", "~"},
		{"/other/path", "/other/path"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := pu.RelativeToHome(tt.input)
			if got != tt.want {
				t.Errorf("ContractPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPathUtil_MakeRelative(t *testing.T) {
	root := t.TempDir()
	photosRoot := filepath.Join(root, "Photos")
	imgPath := filepath.Join(photosRoot, "2023", "img.jpg")
	if err := os.MkdirAll(filepath.Dir(imgPath), 0o755); err != nil {
		t.Fatalf("mkdir temp tree: %v", err)
	}
	if err := os.WriteFile(imgPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	pu := &Resolver{HomeDir: root}

	rel, err := pu.MakeRelative(imgPath, photosRoot)
	if err != nil {
		t.Fatalf("MakeRelative: %v", err)
	}
	if rel != "2023/img.jpg" {
		t.Errorf("MakeRelative = %q, want %q", rel, "2023/img.jpg")
	}
}

func TestPathUtil_MakeAbsolute(t *testing.T) {
	pu := &Resolver{HomeDir: "/home/testuser"}
	got := pu.MakeAbsolute("2023/img.jpg", "~/Photos")
	want := "/home/testuser/Photos/2023/img.jpg"
	if got != want {
		t.Errorf("MakeAbsolute = %q, want %q", got, want)
	}
}

func TestPathUtil_RoundTrip(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	absPath := filepath.Join(home, "Photos", "2023", "img.jpg")
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("mkdir temp tree: %v", err)
	}
	if err := os.WriteFile(absPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	pu := &Resolver{HomeDir: home}
	sourceRoot := filepath.Join(home, "Photos")

	relative, err := pu.MakeRelative(absPath, sourceRoot)
	if err != nil {
		t.Fatalf("MakeRelative: %v", err)
	}

	reconstructed := pu.MakeAbsolute(relative, sourceRoot)
	if reconstructed != absPath {
		t.Errorf("round-trip failed: got %q, want %q", reconstructed, absPath)
	}
}
