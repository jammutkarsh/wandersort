package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
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
	}
}

func TestWalkRoot_DiscoverySmokeTest(t *testing.T) {
	root := createTestTree(t)
	sc := newTestScanner(t)
	filesChan := make(chan FileDiscovery, 200)
	err := sc.walkRoot(context.Background(), uuid.Nil, root, filesChan)
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
			names[i] = d.Path
		}
		t.Fatalf("expected 5 discoveries, got %d: %v", len(discoveries), names)
	}
}

func TestWalkRoot_ContextCancellation(t *testing.T) {
	root := createTestTree(t)
	sc := newTestScanner(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	filesChan := make(chan FileDiscovery, 200)
	err := sc.walkRoot(ctx, uuid.Nil, root, filesChan)
	close(filesChan)

	if err == nil {
		t.Error("walkRoot should return an error when context is cancelled")
	}
}

// ---------------------------------------------------------------------------
// Concurrent walkRoot — multiple goroutines walking same tree
// ---------------------------------------------------------------------------

func TestWalkRoot_ConcurrentWalkers(t *testing.T) {
	root := createTestTree(t)
	sc := newTestScanner(t)

	const walkers = 4
	filesChan := make(chan FileDiscovery, 1000)

	var wg sync.WaitGroup
	for range walkers {
		wg.Go(func() {
			_ = sc.walkRoot(context.Background(), uuid.Nil, root, filesChan)
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
}

// ---------------------------------------------------------------------------
// Run — incremental re-scan against a real database
// ---------------------------------------------------------------------------

func newDBScanner(t *testing.T) (*Scanner, *db.DB) {
	t.Helper()
	d, err := db.New(context.Background(), filepath.Join(t.TempDir(), "test.db"), db.AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d, logger.NewNoopLogger(), 2), d
}

func newSession(t *testing.T, d *db.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO scan_sessions (id, status, root_paths) VALUES (?, 'STARTED', '/tmp')`,
		id.String()); err != nil {
		t.Fatal(err)
	}
	return id
}

type registryRow struct {
	ID         int64  `db:"id"`
	FilePath   string `db:"file_path"`
	ScanStatus string `db:"scan_status"`
	SessionID  string `db:"scan_session_id"`
}

func registryByPath(t *testing.T, d *db.DB, root string) map[string]registryRow {
	t.Helper()
	var rows []registryRow
	if err := d.SQL.Select(&rows,
		`SELECT id, file_path, scan_status, scan_session_id FROM file_registry WHERE source_root = ?`, root); err != nil {
		t.Fatal(err)
	}
	byPath := map[string]registryRow{}
	for _, r := range rows {
		byPath[r.FilePath] = r
	}
	return byPath
}

func TestRunRescan(t *testing.T) {
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

	session1 := newSession(t, d)
	if _, err := sc.Run(ctx, session1, []string{root}); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	d.Writer.Flush()

	rows := registryByPath(t, d, root)
	if len(rows) != 3 {
		t.Fatalf("first scan indexed %d files, want 3", len(rows))
	}

	// Simulate a completed pipeline: metadata rows flip scan_status to HASHED
	// via the trg_file_metadata_hashed trigger; delete.jpg also gets a stale
	// vfs proposal that the sweep must remove
	for _, name := range []string{"keep.jpg", "modify.jpg", "delete.jpg"} {
		if _, err := d.ExecContext(ctx, `INSERT INTO file_metadata (file_hash, file_id) VALUES (?, ?)`,
			"hash-"+name, rows[name].ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO virtual_fs_entries (session_id, file_id, source_path, target_path)
		VALUES (?, ?, '/src/delete.jpg', 'stale/delete.jpg')`,
		session1.String(), rows["delete.jpg"].ID); err != nil {
		t.Fatal(err)
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

	session2 := newSession(t, d)
	if _, err := sc.Run(ctx, session2, []string{root}); err != nil {
		t.Fatalf("re-scan: %v", err)
	}
	d.Writer.Flush()

	rows = registryByPath(t, d, root)
	if len(rows) != 3 {
		t.Fatalf("re-scan left %d files, want 3 (keep, modify, new)", len(rows))
	}
	if got := rows["keep.jpg"].ScanStatus; got != db.StatusHashed {
		t.Errorf("unchanged file scan_status = %s, want HASHED", got)
	}
	if got := rows["modify.jpg"].ScanStatus; got != db.StatusDiscovered {
		t.Errorf("modified file scan_status = %s, want DISCOVERED (re-hash)", got)
	}
	if got := rows["new.jpg"].ScanStatus; got != db.StatusDiscovered {
		t.Errorf("added file scan_status = %s, want DISCOVERED", got)
	}
	if _, ok := rows["delete.jpg"]; ok {
		t.Error("deleted file still present in file_registry after sweep")
	}
	var stale int
	if err := d.SQL.Get(&stale, `SELECT count(*) FROM file_metadata WHERE file_hash = 'hash-delete.jpg'`); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Error("deleted file's metadata row survived the sweep")
	}
	if err := d.SQL.Get(&stale, `SELECT count(*) FROM virtual_fs_entries WHERE target_path = 'stale/delete.jpg'`); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Error("deleted file's vfs entry survived the sweep")
	}
}

func TestRunFailedRootDoesNotSweep(t *testing.T) {
	ctx := context.Background()
	sc, d := newDBScanner(t)
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "photo.jpg"), []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	session1 := newSession(t, d)
	if _, err := sc.Run(ctx, session1, []string{root}); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	d.Writer.Flush()

	// Unplugged-drive scenario: the root vanishes entirely. The walk must fail
	// the scan and the index must survive untouched
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	session2 := newSession(t, d)
	if _, err := sc.Run(ctx, session2, []string{root}); err == nil {
		t.Fatal("scan of a missing root should fail")
	}
	d.Writer.Flush()

	rows := registryByPath(t, d, root)
	if len(rows) != 1 {
		t.Fatalf("missing root swept the index: %d rows left, want 1", len(rows))
	}
	if rows["photo.jpg"].SessionID != session1.String() {
		t.Errorf("surviving row reassigned to session %s", rows["photo.jpg"].SessionID)
	}
}
