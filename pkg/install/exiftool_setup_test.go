package install

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// realExiftoolDir is the test-deps bin folder holding the real exiftool the
// app installs, installing it there once per machine if missing. Its folder
// lookup mirrors installtest.depsDir, which this package can't import (cycle).
func realExiftoolDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("WANDERSORT_TEST_DEPS_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home dir: %v", err)
		}
		dir = filepath.Join(home, ".wandersort")
	}
	binDir := filepath.Join(dir, "bin")
	if _, err := setupExiftool(context.Background(), logger.NewNoopLogger(), binDir, nil); err != nil {
		t.Skipf("exiftool unavailable (offline?): %v", err)
	}
	return binDir
}

func TestVersionAtLeast(t *testing.T) {
	tests := []struct {
		name    string
		ver     string
		want    bool
		wantErr bool
	}{
		{"exact match", exiftoolVersion, true, false},
		{"newer major", "14.00", true, false},
		{"newer minor same major", "13.60", true, false},
		{"older minor same major", "13.10", false, false},
		{"older major", "12.99", false, false},
		{"malformed - no dot", "13", false, true},
		{"malformed - non-numeric", "a.b", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := versionAtLeast(tt.ver)
			if (err != nil) != tt.wantErr {
				t.Fatalf("versionAtLeast(%q) error = %v, wantErr %v", tt.ver, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("versionAtLeast(%q) = %v, want %v", tt.ver, got, tt.want)
			}
		})
	}
}

func TestCheckVersionOfRealExiftool(t *testing.T) {
	ok, err := checkVersion(filepath.Join(realExiftoolDir(t), exiftoolBin()), logger.NewNoopLogger())
	if err != nil || !ok {
		t.Errorf("checkVersion() on the installed exiftool = %v, %v; want true", ok, err)
	}
}

func TestCheckVersionMissingBinary(t *testing.T) {
	_, err := checkVersion(filepath.Join(t.TempDir(), "does-not-exist"), logger.NewNoopLogger())
	if err == nil {
		t.Error("checkVersion() on a missing binary = nil error, want one")
	}
}

func TestFindExiftoolUsesBinDir(t *testing.T) {
	binDir := realExiftoolDir(t)

	got, err := findExiftool(logger.NewNoopLogger(), binDir)
	if err != nil {
		t.Fatalf("findExiftool() error = %v", err)
	}
	want := filepath.Join(binDir, exiftoolBin())
	if got != want {
		t.Errorf("findExiftool() = %q, want %q", got, want)
	}
}

func TestFindExiftoolNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := findExiftool(logger.NewNoopLogger(), t.TempDir()); err == nil {
		t.Error("findExiftool() with nothing installed = nil error, want one")
	}
}

// buildTarZst writes a tar.zst at dest containing files (name -> content),
// mirroring the flat layout publish-r2.yml produces.
func buildTarZst(t *testing.T, dest string, files map[string]string) {
	t.Helper()
	f, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw, err := zstd.NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTarZst(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "exiftool.tar.zst")
	buildTarZst(t, archive, map[string]string{
		"exiftool":     "#!/bin/sh\necho fake\n",
		"lib/data.txt": "some bundled data",
	})

	destDir := t.TempDir()
	if err := extractTarZst(archive, destDir); err != nil {
		t.Fatalf("extractTarZst() error = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(destDir, "exiftool"))
	if err != nil || string(got) != "#!/bin/sh\necho fake\n" {
		t.Errorf("extracted exiftool = %q, %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(destDir, "lib/data.txt"))
	if err != nil || string(got) != "some bundled data" {
		t.Errorf("extracted lib/data.txt = %q, %v", got, err)
	}
}

func TestExtractTarZstBadArchive(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "not-a-tar-zst")
	if err := os.WriteFile(bad, []byte("not zst data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractTarZst(bad, t.TempDir()); err == nil {
		t.Error("extractTarZst() on a non-zst file = nil error, want one")
	}
}

func TestExtractTarZstMissingFile(t *testing.T) {
	if err := extractTarZst(filepath.Join(t.TempDir(), "missing.tar.zst"), t.TempDir()); err == nil {
		t.Error("extractTarZst() on a missing archive = nil error, want one")
	}
}
