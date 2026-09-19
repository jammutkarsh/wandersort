// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/volume"
	"golang.org/x/text/unicode/norm"
)

// ---------------------------------------------------------------------------
// walkRoot — integration test with a real temp directory tree
// ---------------------------------------------------------------------------

// createTestTree builds a directory tree under t.TempDir() and returns the root
//
//	root/
//	  photos/
//	    IMG_001.jpg      (1 KB)
//	    IMG_002.heic     (2 KB)
//	    IMG_002.aae      (128 B)
//	    raw/
//	      _MG_100.cr2    (4 KB)
//	  videos/
//	    clip.mp4         (8 KB)
//	  junk/
//	    readme.txt
//	    .DS_Store
//	  .git/
//	    HEAD
func createTestTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	dirs := []string{
		"photos", "photos/raw", "videos", "junk", ".git",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	files := map[string]int{
		"photos/IMG_001.jpg":     1024,
		"photos/IMG_002.heic":    2048,
		"photos/IMG_002.aae":     128,
		"photos/raw/_MG_100.cr2": 4096,
		"videos/clip.mp4":        8192,
		"junk/readme.txt":        64,
		"junk/.DS_Store":         32,
		".git/HEAD":              23,
	}
	for name, size := range files {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

// newTestScanner constructs a Scanner with a noop logger for testing
func newTestScanner(t *testing.T) *Scanner {
	t.Helper()
	return &Scanner{
		classifier: classifier.NewFileClassifier(),
		log:        logger.NewNoopLogger(),
		path:       &path.Resolver{HomeDir: "/tmp"},
		volumes:    volume.New(),
	}
}

func TestScanner(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"WalkRoot_DiscoverySmokeTest", func(t *testing.T) {
			root := createTestTree(t)
			sc := newTestScanner(t)
			filesChan := make(chan FileDiscovery, 200)
			err := sc.walkRoot(context.Background(), root, "", filesChan)
			close(filesChan)
			if err != nil {
				t.Fatalf("walkRoot: %v", err)
			}

			// Collect discoveries
			var discoveries []FileDiscovery
			for d := range filesChan {
				discoveries = append(discoveries, d)
			}

			// Expected: IMG_001.jpg, IMG_002.heic, IMG_002.aae, _MG_100.cr2, clip.mp4
			// NOT expected: readme.txt (unsupported), .DS_Store (ignored), .git/HEAD (ignored dir)
			if len(discoveries) != 5 {
				names := make([]string, len(discoveries))
				for i, d := range discoveries {
					names[i] = d.Name
				}
				t.Fatalf("expected 5 discoveries, got %d: %v", len(discoveries), names)
			}
		}},
		{"WalkRoot_ContextCancellation", func(t *testing.T) {
			root := createTestTree(t)
			sc := newTestScanner(t)

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // cancel immediately

			filesChan := make(chan FileDiscovery, 200)
			err := sc.walkRoot(ctx, root, "", filesChan)
			close(filesChan)

			if err == nil {
				t.Error("walkRoot should return an error when context is cancelled")
			}
		}},
		// ---------------------------------------------------------------------------
		// Concurrent walkRoot — multiple goroutines walking same tree
		// ---------------------------------------------------------------------------
		{"WalkRoot_ConcurrentWalkers", func(t *testing.T) {
			root := createTestTree(t)
			sc := newTestScanner(t)

			const walkers = 4
			filesChan := make(chan FileDiscovery, 1000)

			var wg sync.WaitGroup
			for range walkers {
				wg.Go(func() {
					_ = sc.walkRoot(context.Background(), root, "", filesChan)
				})
			}

			go func() {
				wg.Wait()
				close(filesChan)
			}()

			var total int
			for range filesChan {
				total++
			}

			// Each walker discovers the same 5 files
			expected := 5 * walkers
			if total != expected {
				t.Errorf("total discoveries = %d, want %d", total, expected)
			}
		}},
		{"RunRescan", func(t *testing.T) {
			ctx := context.Background()
			sc, d := newDBScanner(t)
			root := t.TempDir()

			for name, content := range map[string]string{
				"keep.jpg":   "unchanged bytes",
				"modify.jpg": "original bytes",
				"delete.jpg": "doomed bytes",
			} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := sc.Run(ctx, []string{root}, false); err != nil {
				t.Fatalf("first scan: %v", err)
			}
			d.Writer.Flush()

			rows := registryByName(t, d)
			if len(rows) != 3 {
				t.Fatalf("first scan indexed %d files, want 3", len(rows))
			}

			// Simulate a completed pipeline: the metadata phase writes the row
			// and marks the file ANALYZED
			for _, name := range []string{"keep.jpg", "modify.jpg", "delete.jpg"} {
				if _, err := d.ExecContext(ctx, `INSERT INTO file_metadata (file_hash, file_id) VALUES (?, ?)`,
					"hash-"+name, rows[name].ID); err != nil {
					t.Fatal(err)
				}
				if _, err := d.ExecContext(ctx, `UPDATE file_registry SET scan_status = ? WHERE id = ?`,
					db.StatusAnalyzed, rows[name].ID); err != nil {
					t.Fatal(err)
				}
			}

			// Mutate the tree: touch one file, remove one, add one
			if err := os.WriteFile(filepath.Join(root, "modify.jpg"), []byte("changed bytes, new size"), 0o644); err != nil {
				t.Fatal(err)
			}
			past := time.Now().Add(-time.Hour)
			if err := os.Chtimes(filepath.Join(root, "modify.jpg"), past, past); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(root, "delete.jpg")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "new.jpg"), []byte("fresh bytes"), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := sc.Run(ctx, []string{root}, false); err != nil {
				t.Fatalf("re-scan: %v", err)
			}
			d.Writer.Flush()

			rows = registryByName(t, d)
			if len(rows) != 3 {
				t.Fatalf("re-scan left %d rows, want 3 (keep, modify, new; delete hard-deleted)", len(rows))
			}
			if got := rows["keep.jpg"].Status; got != db.StatusAnalyzed {
				t.Errorf("unchanged file scan_status = %s, want ANALYZED", got)
			}
			if got := rows["modify.jpg"].Status; got != db.StatusDiscovered {
				t.Errorf("modified file scan_status = %s, want DISCOVERED (re-read)", got)
			}
			if got := rows["new.jpg"].Status; got != db.StatusDiscovered {
				t.Errorf("added file scan_status = %s, want DISCOVERED", got)
			}
			if _, ok := rows["delete.jpg"]; ok {
				t.Error("vanished file was not hard-deleted by the sweep")
			}
			var metaCount int
			if err := d.SQL.Get(&metaCount, `SELECT COUNT(*) FROM file_metadata WHERE file_hash = 'hash-delete.jpg'`); err != nil {
				t.Fatal(err)
			}
			if metaCount != 0 {
				t.Error("vanished file's metadata row survived the sweep")
			}

			// A reappearing file is a fresh row, not a resurrection — its old
			// row (and metadata) were already gone
			if err := os.WriteFile(filepath.Join(root, "delete.jpg"), []byte("doomed bytes"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := sc.Run(ctx, []string{root}, false); err != nil {
				t.Fatalf("resurrect scan: %v", err)
			}
			d.Writer.Flush()
			rows = registryByName(t, d)
			if got := rows["delete.jpg"].Status; got != db.StatusDiscovered {
				t.Errorf("reappeared file scan_status = %s, want DISCOVERED", got)
			}
		}},
		// TestRunForceRescan: an unchanged file (same size/mtime) normally keeps
		// its ANALYZED status; force=true resets it back to DISCOVERED anyway, so
		// a later metadata phase re-reads it instead of skipping it
		{"RunForceRescan", func(t *testing.T) {
			ctx := context.Background()
			sc, d := newDBScanner(t)
			root := t.TempDir()

			if err := os.WriteFile(filepath.Join(root, "keep.jpg"), []byte("unchanged bytes"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := sc.Run(ctx, []string{root}, false); err != nil {
				t.Fatalf("first scan: %v", err)
			}
			d.Writer.Flush()

			rows := registryByName(t, d)
			if _, err := d.ExecContext(ctx, `UPDATE file_registry SET scan_status = ? WHERE id = ?`,
				db.StatusAnalyzed, rows["keep.jpg"].ID); err != nil {
				t.Fatal(err)
			}

			// Nothing on disk changes between scans
			if _, err := sc.Run(ctx, []string{root}, true); err != nil {
				t.Fatalf("forced re-scan: %v", err)
			}
			d.Writer.Flush()

			rows = registryByName(t, d)
			if got := rows["keep.jpg"].Status; got != db.StatusDiscovered {
				t.Errorf("unchanged file scan_status = %s, want DISCOVERED (forced)", got)
			}
		}},
		// RunRescanPreservesNFDName guards against forcing a disk-given name
		// to NFC on write, which folds it the same wrong way on
		// every scan, so the rescan itself never marks it vanished — what
		// actually broke was opening the stored NFC spelling against the
		// real NFD-named file on Linux, where lookup is byte-exact. Kept
		// unfolded, the name matches on disk (and here, byte for byte on
		// rescan) instead of just failing to open later.
		{"RunRescanPreservesNFDName", func(t *testing.T) {
			ctx := context.Background()
			sc, d := newDBScanner(t)
			root := t.TempDir()

			nfdName := norm.NFD.String("Café.jpg")
			if err := os.WriteFile(filepath.Join(root, nfdName), []byte("bytes"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := sc.Run(ctx, []string{root}, false); err != nil {
				t.Fatalf("first scan: %v", err)
			}
			d.Writer.Flush()
			if _, err := sc.Run(ctx, []string{root}, false); err != nil {
				t.Fatalf("second scan: %v", err)
			}
			d.Writer.Flush()

			var rows []struct {
				FileName string `db:"file_name"`
			}
			if err := d.SQL.Select(&rows, `SELECT file_name FROM file_registry`); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("got %d rows after two scans, want 1 (unchanged NFD-named file marked vanished on rescan): %+v", len(rows), rows)
			}
			if rows[0].FileName != nfdName {
				t.Errorf("file_name = %q, want unchanged %q (no NFC folding)", rows[0].FileName, nfdName)
			}
		}},
		// TestSweepFilesystemRoot: sweeping the filesystem root itself must still
		// cover every stored file_dir — the naive prefix range ["//", "/0") contains
		// no path at all, so an unswept "/" scan would silently keep ghosts alive
		{"SweepFilesystemRoot", func(t *testing.T) {
			ctx := context.Background()
			sc, d := newDBScanner(t)

			// SeedFile stamps a fixed 2024-01-01 last_seen_at, well before "now"
			dbtest.SeedFile(t, d, 1, "/photos/trips", "gone.jpg", 10)
			dbtest.SeedFile(t, d, 2, "/", "root.jpg", 10)

			if err := sc.sweep(ctx, time.Now(), "/"); err != nil {
				t.Fatalf("sweep: %v", err)
			}

			rows := registryByName(t, d)
			for _, name := range []string{"gone.jpg", "root.jpg"} {
				if _, ok := rows[name]; ok {
					t.Errorf("%s not swept under filesystem root", name)
				}
			}
		}},
		{"RunFailedRootDoesNotSweep", func(t *testing.T) {
			ctx := context.Background()
			sc, d := newDBScanner(t)
			root := t.TempDir()

			if err := os.WriteFile(filepath.Join(root, "photo.jpg"), []byte("bytes"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := sc.Run(ctx, []string{root}, false); err != nil {
				t.Fatalf("first scan: %v", err)
			}
			d.Writer.Flush()
			seenAfterFirst := registryByName(t, d)["photo.jpg"].LastSeenAt

			// Unplugged-drive scenario: the root vanishes entirely. The scan must fail
			// and the index must survive untouched
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			if _, err := sc.Run(ctx, []string{root}, false); err == nil {
				t.Fatal("scan of a missing root should fail")
			}
			d.Writer.Flush()

			rows := registryByName(t, d)
			if len(rows) != 1 {
				t.Fatalf("missing root swept the index: %d rows left, want 1", len(rows))
			}
			if _, ok := rows["photo.jpg"]; !ok {
				t.Error("missing root deleted a file it never scanned")
			}
			if rows["photo.jpg"].LastSeenAt != seenAfterFirst {
				t.Errorf("surviving row's last_seen_at changed to %s, want unchanged %s", rows["photo.jpg"].LastSeenAt, seenAfterFirst)
			}
		}},
		// TestRunPartialRootFailureSweepsOnlyCleanRoots: with two disjoint roots where
		// one vanishes (unplugged drive), the surviving root still sweeps its own
		// vanished files, the dead root's index stays untouched, and the scan fails
		{"RunPartialRootFailureSweepsOnlyCleanRoots", func(t *testing.T) {
			ctx := context.Background()
			sc, d := newDBScanner(t)
			rootA, rootB := t.TempDir(), t.TempDir()

			for path, content := range map[string]string{
				filepath.Join(rootA, "keep.jpg"):  "kept bytes",
				filepath.Join(rootA, "gone.jpg"):  "doomed bytes",
				filepath.Join(rootB, "photo.jpg"): "drive bytes",
			} {
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := sc.Run(ctx, []string{rootA, rootB}, false); err != nil {
				t.Fatalf("first scan: %v", err)
			}
			d.Writer.Flush()
			seenAfterFirst := registryByName(t, d)["photo.jpg"].LastSeenAt

			if err := os.Remove(filepath.Join(rootA, "gone.jpg")); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(rootB); err != nil {
				t.Fatal(err)
			}
			if _, err := sc.Run(ctx, []string{rootA, rootB}, false); err == nil {
				t.Fatal("scan with a missing root should fail")
			}
			d.Writer.Flush()

			rows := registryByName(t, d)
			if _, ok := rows["gone.jpg"]; ok {
				t.Error("clean root's vanished file was not swept")
			}
			if _, ok := rows["keep.jpg"]; !ok {
				t.Error("clean root's live file was swept")
			}
			if _, ok := rows["photo.jpg"]; !ok {
				t.Error("failed root's file was swept despite the walk never running")
			}
			if rows["photo.jpg"].LastSeenAt != seenAfterFirst {
				t.Errorf("failed root's row last_seen_at changed to %s, want unchanged %s", rows["photo.jpg"].LastSeenAt, seenAfterFirst)
			}
		}},
		// TestSweepDeletesDependentRowsInFKOrder: a vanished file's metadata and
		// vfs-plan rows go with it — no grace window and no leftovers for
		// foreign_keys=ON to reject
		{"SweepDeletesDependentRows", func(t *testing.T) {
			ctx := context.Background()
			sc, d := newDBScanner(t)

			dbtest.SeedFile(t, d, 1, "/gone", "vanished.jpg", 10)
			if _, err := d.ExecContext(ctx,
				`INSERT INTO file_metadata (file_hash, file_id) VALUES ('vanished-hash', 1)`); err != nil {
				t.Fatal(err)
			}
			if _, err := d.ExecContext(ctx, `
				INSERT INTO virtual_fs_entries (file_id, source_path, target_path)
				VALUES (1, '/gone/vanished.jpg', 'stale/vanished.jpg')`); err != nil {
				t.Fatal(err)
			}

			if err := sc.sweep(ctx, time.Now(), "/gone"); err != nil {
				t.Fatalf("sweep: %v", err)
			}

			var leftovers int
			if err := d.SQL.Get(&leftovers, `SELECT count(*) FROM file_registry WHERE id = 1`); err != nil {
				t.Fatal(err)
			}
			if leftovers != 0 {
				t.Error("swept file's registry row survived")
			}
			if err := d.SQL.Get(&leftovers, `SELECT count(*) FROM file_metadata WHERE file_id = 1`); err != nil {
				t.Fatal(err)
			}
			if leftovers != 0 {
				t.Error("swept file left a metadata row behind")
			}
			if err := d.SQL.Get(&leftovers, `SELECT count(*) FROM virtual_fs_entries WHERE file_id = 1`); err != nil {
				t.Fatal(err)
			}
			if leftovers != 0 {
				t.Error("swept file left a vfs row behind")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

// ---------------------------------------------------------------------------
// Run — incremental re-scan against a real database
// ---------------------------------------------------------------------------

func newDBScanner(t *testing.T) (*Scanner, *db.DB) {
	t.Helper()
	d := dbtest.New(t)
	return New(d, logger.NewNoopLogger(), 2), d
}

type registryRow struct {
	ID         int64  `db:"id"`
	FileName   string `db:"file_name"`
	Status     string `db:"scan_status"`
	LastSeenAt string `db:"last_seen_at"`
}

// registryByName keys every registry row by file name; test trees use unique
// names so the file_dir does not matter for lookups
func registryByName(t *testing.T, d *db.DB) map[string]registryRow {
	t.Helper()
	var rows []registryRow
	if err := d.SQL.Select(&rows,
		`SELECT id, file_name, scan_status, last_seen_at FROM file_registry`); err != nil {
		t.Fatal(err)
	}
	byName := map[string]registryRow{}
	for _, r := range rows {
		byName[r.FileName] = r
	}
	return byName
}
