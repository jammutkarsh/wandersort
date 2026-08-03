// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// fakeExiftool writes an executable shell script at path that prints ver to
// stdout when run with "-ver" — standing in for the real binary so
// checkVersion/findExiftool are testable without a live download.
func fakeExiftool(t *testing.T, path, ver string) {
	t.Helper()
	if runtime.GOOS == windows {
		t.Skip("fake exiftool script is a shell script; unix-only")
	}
	script := "#!/bin/sh\necho '" + ver + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake exiftool: %v", err)
	}
}

func TestCheckVersion(t *testing.T) {
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
			bin := filepath.Join(t.TempDir(), "exiftool")
			fakeExiftool(t, bin, tt.ver)

			got, err := checkVersion(bin, logger.NewNoopLogger())
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkVersion() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("checkVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckVersionMissingBinary(t *testing.T) {
	_, err := checkVersion(filepath.Join(t.TempDir(), "does-not-exist"), logger.NewNoopLogger())
	if err == nil {
		t.Error("checkVersion() on a missing binary = nil error, want one")
	}
}

func TestFindExiftoolPrefersBinDirOverMissingPath(t *testing.T) {
	binDir := t.TempDir()
	fakeExiftool(t, filepath.Join(binDir, exiftoolBin()), exiftoolVersion)

	got, err := findExiftool(logger.NewNoopLogger(), binDir)
	if err != nil {
		t.Fatalf("findExiftool() error = %v", err)
	}
	want := filepath.Join(binDir, exiftoolBin())
	if got != want {
		t.Errorf("findExiftool() = %q, want %q", got, want)
	}
}

func TestFindExiftoolRejectsOutdatedBinDir(t *testing.T) {
	binDir := t.TempDir()
	fakeExiftool(t, filepath.Join(binDir, exiftoolBin()), "1.0")

	if _, err := findExiftool(logger.NewNoopLogger(), binDir); err == nil {
		t.Error("findExiftool() with an outdated binary = nil error, want one (must fall through to install)")
	}
}

func TestFindExiftoolNotFound(t *testing.T) {
	if _, err := findExiftool(logger.NewNoopLogger(), t.TempDir()); err == nil {
		t.Error("findExiftool() with nothing installed = nil error, want one")
	}
}

// buildTarGz writes a tar.gz at dest containing files (name -> content),
// mirroring the flat layout publish-r2.yml produces.
func buildTarGz(t *testing.T, dest string, files map[string]string) {
	t.Helper()
	f, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
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
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTarGz(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "exiftool.tar.gz")
	buildTarGz(t, archive, map[string]string{
		"exiftool":     "#!/bin/sh\necho fake\n",
		"lib/data.txt": "some bundled data",
	})

	destDir := t.TempDir()
	if err := extractTarGz(archive, destDir); err != nil {
		t.Fatalf("extractTarGz() error = %v", err)
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

func TestExtractTarGzBadArchive(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "not-a-tar-gz")
	if err := os.WriteFile(bad, []byte("not gzip data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGz(bad, t.TempDir()); err == nil {
		t.Error("extractTarGz() on a non-gzip file = nil error, want one")
	}
}

func TestExtractTarGzMissingFile(t *testing.T) {
	if err := extractTarGz(filepath.Join(t.TempDir(), "missing.tar.gz"), t.TempDir()); err == nil {
		t.Error("extractTarGz() on a missing archive = nil error, want one")
	}
}
