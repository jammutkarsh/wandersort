package review

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The size cap is what keeps a peek at a 600GB folder from filling the disk,
// so it gets the one check: copying stops once maxBytes is reached.
func TestCopyFilesStopsAtMaxBytes(t *testing.T) {
	src := t.TempDir()
	dest := filepath.Join(t.TempDir(), "preview")

	var paths []string
	for _, name := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		p := filepath.Join(src, name)
		if err := os.WriteFile(p, make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	// 150 bytes admits the first file, then the second (the cap is checked
	// before each copy, not after), and stops before the third
	copied, err := copyFiles(context.Background(), paths, dest, 150)
	if err != nil {
		t.Fatalf("copyFiles: %v", err)
	}
	if copied != 2 {
		t.Errorf("copied = %d, want 2", copied)
	}

	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("dest holds %d files, want 2", len(entries))
	}
}

// TestCopyFilesMissingSourceReturnsError covers a source that vanished between
// listing and copying — copyFile's os.Open failure must surface, not panic or
// silently skip.
func TestCopyFilesMissingSourceReturnsError(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "preview")
	_, err := copyFiles(context.Background(), []string{filepath.Join(t.TempDir(), "gone.jpg")}, dest, maxPreviewBytes)
	if err == nil {
		t.Fatal("expected an error for a missing source file")
	}
}

// TestCopyFilesStopsOnCanceledContext covers the ctx.Err() check between files
// — a canceled context stops the copy loop rather than running to completion.
func TestCopyFilesStopsOnCanceledContext(t *testing.T) {
	src := t.TempDir()
	dest := filepath.Join(t.TempDir(), "preview")
	p := filepath.Join(src, "a.jpg")
	if err := os.WriteFile(p, make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	copied, err := copyFiles(ctx, []string{p}, dest, maxPreviewBytes)
	if err == nil {
		t.Fatal("expected the canceled context's error")
	}
	if copied != 0 {
		t.Errorf("copied = %d, want 0 before any file is touched", copied)
	}
}
