package scanner

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// newTestScanner builds a Scanner wired to outputPath without a live DB.
func newTestScanner(t *testing.T, outputPath string) *Scanner {
	t.Helper()
	pathUtil, err := NewPathUtil()
	if err != nil {
		t.Fatalf("NewPathUtil: %v", err)
	}
	return &Scanner{
		classifier: NewFileClassifier(),
		log:        logger.NewNoopLogger(),
		pathUtil:   pathUtil,
		outputPath: outputPath,
	}
}

// seedDir creates named files inside a new temp dir and returns the dir path.
func seedDir(t *testing.T, names []string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	return dir
}

// reportLines reads the unsupported report for sessionID and returns the
// non-blank, non-comment lines (the actual file paths).
func reportLines(t *testing.T, outputPath string, sessionID uuid.UUID) []string {
	t.Helper()
	p := filepath.Join(outputPath, "unsupported_files_"+sessionID.String()+".txt")
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("report not found at %s: %v", p, err)
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// reportExists returns true when the expected report file is present on disk.
func reportExists(outputPath string, sessionID uuid.UUID) bool {
	p := filepath.Join(outputPath, "unsupported_files_"+sessionID.String()+".txt")
	_, err := os.Stat(p)
	return err == nil
}

// doWalk calls walkRoot, drains the output channel, and returns the scanState
// so callers can inspect counters and pass it to writeUnsupportedFiles.
func doWalk(t *testing.T, sc *Scanner, root string) *scanState {
	t.Helper()
	st := &scanState{}
	ch := make(chan FileDiscovery, 256)
	go func() {
		for range ch {
		}
	}()
	if err := sc.walkRoot(context.Background(), root, root, ch, st); err != nil {
		t.Fatalf("walkRoot: %v", err)
	}
	close(ch)
	return st
}

// ---------------------------------------------------------------------------
// Test 1: unsupported files → report created, listing only those files
// ---------------------------------------------------------------------------

func TestUnsupportedFilesReportCreated(t *testing.T) {
	sourceDir := seedDir(t, []string{"photo.jpg", "clip.mp4", "document.pdf", "notes.docx"})
	outputDir := t.TempDir()

	sc := newTestScanner(t, outputDir)
	sessionID := uuid.New()

	st := doWalk(t, sc, sourceDir)
	sc.writeUnsupportedFiles(sessionID, st)

	lines := reportLines(t, outputDir, sessionID)
	if len(lines) == 0 {
		t.Fatal("expected unsupported paths in report, got none")
	}

	reported := make(map[string]bool, len(lines))
	for _, l := range lines {
		reported[filepath.Base(l)] = true
	}

	// Unsupported files must be present.
	for _, want := range []string{"document.pdf", "notes.docx"} {
		if !reported[want] {
			t.Errorf("expected %s in report", want)
		}
	}

	// Supported files must be absent.
	for _, bad := range []string{"photo.jpg", "clip.mp4"} {
		if reported[bad] {
			t.Errorf("supported file %s must not appear in unsupported report", bad)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 2: all files supported → no report file created
// ---------------------------------------------------------------------------

func TestNoReportWhenAllSupported(t *testing.T) {
	sourceDir := seedDir(t, []string{"photo.jpg", "clip.mp4", "raw.arw", "edit.xmp"})
	outputDir := t.TempDir()

	sc := newTestScanner(t, outputDir)
	sessionID := uuid.New()

	st := doWalk(t, sc, sourceDir)
	sc.writeUnsupportedFiles(sessionID, st)

	if reportExists(outputDir, sessionID) {
		t.Error("report must not be created when all files have supported extensions")
	}
}

// ---------------------------------------------------------------------------
// Test 3: system-ignored files (.DS_Store etc.) must not appear in report
// ---------------------------------------------------------------------------

func TestIgnoredFilesNotInReport(t *testing.T) {
	sourceDir := seedDir(t, []string{".DS_Store", "Thumbs.db", "document.pdf"})
	outputDir := t.TempDir()

	sc := newTestScanner(t, outputDir)
	sessionID := uuid.New()

	st := doWalk(t, sc, sourceDir)
	sc.writeUnsupportedFiles(sessionID, st)

	lines := reportLines(t, outputDir, sessionID)

	for _, l := range lines {
		base := filepath.Base(l)
		if base == ".DS_Store" || base == "Thumbs.db" {
			t.Errorf("system-ignored file %s must not appear in report", base)
		}
	}

	found := false
	for _, l := range lines {
		if filepath.Base(l) == "document.pdf" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected document.pdf in unsupported report")
	}
}

// ---------------------------------------------------------------------------
// Test 4: unsupported list is reset between scans (no cross-scan leakage)
// ---------------------------------------------------------------------------

// TestUnsupportedListResetBetweenScans verifies that each call to walkRoot
// starts from a clean scanState — no paths can leak from one scan to the next.
func TestUnsupportedListResetBetweenScans(t *testing.T) {
	dir1 := seedDir(t, []string{"document.pdf"}) // has unsupported file
	dir2 := seedDir(t, []string{"photo.jpg"})    // all supported
	outputDir := t.TempDir()

	sc := newTestScanner(t, outputDir)

	// First scan — produces one unsupported path.
	id1 := uuid.New()
	st1 := doWalk(t, sc, dir1)
	sc.writeUnsupportedFiles(id1, st1)

	if !reportExists(outputDir, id1) {
		t.Error("first scan should produce a report for document.pdf")
	}

	// Second scan uses a completely separate scanState — no manual reset needed.
	id2 := uuid.New()
	st2 := doWalk(t, sc, dir2)
	sc.writeUnsupportedFiles(id2, st2)

	if reportExists(outputDir, id2) {
		t.Error("second scan (all-supported) must not create a report")
	}
}
