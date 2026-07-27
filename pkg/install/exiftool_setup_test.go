// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// TestExtractTarGzRoundTrips covers the archive layout CI produces: files and
// directories flat at the root, extracted directly into destDir with no
// per-platform unwrapping.
func TestExtractTarGzRoundTrips(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "exiftool.tar.gz")

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("#!/bin/sh\necho fake\n")
	if err := tw.WriteHeader(&tar.Header{Name: "exiftool", Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(dir, "out")
	if err := extractTarGz(archive, destDir); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(destDir, "exiftool"))
	if err != nil {
		t.Fatalf("extracted file missing: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("extracted content = %q, want %q", got, content)
	}
}

// TestCheckVersionComparesMajorMinor covers the version-gate that decides
// whether an already-installed exiftool is used as-is or replaced.
func TestCheckVersionComparesMajorMinor(t *testing.T) {
	if runtime.GOOS == windows {
		t.Skip("fake binary is a shell script, unix-only")
	}
	fake := func(t *testing.T, version string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "fake-exiftool")
		script := "#!/bin/sh\necho " + version + "\n"
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}

	log := logger.NewNoopLogger()
	tests := []struct {
		version string
		want    bool
	}{
		{exiftoolVersion, true},
		{"99.99", true},
		{"1.00", false},
	}
	for _, tt := range tests {
		ok, err := checkVersion(fake(t, tt.version), log)
		if err != nil {
			t.Fatalf("checkVersion(%q): %v", tt.version, err)
		}
		if ok != tt.want {
			t.Errorf("checkVersion(%q) = %v, want %v", tt.version, ok, tt.want)
		}
	}
}
