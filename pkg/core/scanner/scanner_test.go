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
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/volume"
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

func TestWalkRoot_DiscoverySmokeTest(t *testing.T) {
	root := createTestTree(t)
	sc := newTestScanner(t)
	filesChan := make(chan FileDiscovery, 200)
	err := sc.walkRoot(context.Background(), uuid.Nil, root, "", filesChan)
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
}

func TestWalkRoot_ContextCancellation(t *testing.T) {
	root := createTestTree(t)
	sc := newTestScanner(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	filesChan := make(chan FileDiscovery, 200)
	err := sc.walkRoot(ctx, uuid.Nil, root, "", filesChan)
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
			_ = sc.walkRoot(context.Background(), uuid.Nil, root, "", filesChan)
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
	d := dbtest.New(t)
	return New(d, logger.NewNoopLogger(), 2), d
}

type registryRow struct {
	ID        int64   `db:"id"`
	FileName  string  `db:"file_name"`
	Status    string  `db:"scan_status"`
	SessionID string  `db:"scan_session_id"`
	DeletedAt *string `db:"deleted_at"`
}

// registryByName keys every registry row by file name; test trees use unique
// names so the file_dir does not matter for lookups
func registryByName(t *testing.T, d *db.DB) map[string]registryRow {
	t.Helper()
	var rows []registryRow
	if err := d.SQL.Select(&rows,
		`SELECT id, file_name, scan_status, scan_session_id, deleted_at FROM file_registry`); err != nil {
		t.Fatal(err)
	}
	byName := map[string]registryRow{}
	for _, r := range rows {
		byName[r.FileName] = r
	}
	return byName
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

	session1 := dbtest.NewSession(t, d, db.StatusStarted)
	if _, err := sc.Run(ctx, session1, []string{root}); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	d.Writer.Flush()

	rows := registryByName(t, d)
	if len(rows) != 3 {
		t.Fatalf("first scan indexed %d files, want 3", len(rows))
	}

	// Simulate a completed pipeline: metadata rows flip scan_status to HASHED
	// via the trg_file_metadata_hashed trigger
	for _, name := range []string{"keep.jpg", "modify.jpg", "delete.jpg"} {
		if _, err := d.ExecContext(ctx, `INSERT INTO file_metadata (file_hash, file_id) VALUES (?, ?)`,
			"hash-"+name, rows[name].ID); err != nil {
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

	session2 := dbtest.NewSession(t, d, db.StatusStarted)
	if _, err := sc.Run(ctx, session2, []string{root}); err != nil {
		t.Fatalf("re-scan: %v", err)
	}
	d.Writer.Flush()

	rows = registryByName(t, d)
	if len(rows) != 4 {
		t.Fatalf("re-scan left %d rows, want 4 (keep, modify, new + soft-deleted delete)", len(rows))
	}
	if got := rows["keep.jpg"].Status; got != db.StatusHashed {
		t.Errorf("unchanged file scan_status = %s, want HASHED", got)
	}
	if got := rows["modify.jpg"].Status; got != db.StatusDiscovered {
		t.Errorf("modified file scan_status = %s, want DISCOVERED (re-hash)", got)
	}
	if got := rows["new.jpg"].Status; got != db.StatusDiscovered {
		t.Errorf("added file scan_status = %s, want DISCOVERED", got)
	}
	for _, name := range []string{"keep.jpg", "modify.jpg", "new.jpg"} {
		if rows[name].DeletedAt != nil {
			t.Errorf("live file %s carries deleted_at %v", name, *rows[name].DeletedAt)
		}
	}
	if rows["delete.jpg"].DeletedAt == nil {
		t.Error("vanished file was not soft-deleted by the sweep")
	}

	// The vanished file resurrects when it reappears on disk unchanged
	if err := os.WriteFile(filepath.Join(root, "delete.jpg"), []byte("doomed bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	session3 := dbtest.NewSession(t, d, db.StatusStarted)
	if _, err := sc.Run(ctx, session3, []string{root}); err != nil {
		t.Fatalf("resurrect scan: %v", err)
	}
	d.Writer.Flush()
	rows = registryByName(t, d)
	if rows["delete.jpg"].DeletedAt != nil {
		t.Error("reappeared file is still marked deleted")
	}
}

// TestSweepFilesystemRoot: sweeping the filesystem root itself must still
// cover every stored file_dir — the naive prefix range ["//", "/0") contains
// no path at all, so an unswept "/" scan would silently keep ghosts alive
func TestSweepFilesystemRoot(t *testing.T) {
	ctx := context.Background()
	sc, d := newDBScanner(t)

	stale := dbtest.NewSession(t, d, db.StatusCompleted)
	dbtest.SeedFile(t, d, stale, 1, "/photos/trips", "gone.jpg", 10)
	dbtest.SeedFile(t, d, stale, 2, "/", "root.jpg", 10)

	current := dbtest.NewSession(t, d, db.StatusStarted)
	if err := sc.sweep(ctx, current, "/"); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	rows := registryByName(t, d)
	for _, name := range []string{"gone.jpg", "root.jpg"} {
		if rows[name].DeletedAt == nil {
			t.Errorf("%s not swept under filesystem root", name)
		}
	}
}

func TestRunFailedRootDoesNotSweep(t *testing.T) {
	ctx := context.Background()
	sc, d := newDBScanner(t)
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "photo.jpg"), []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	session1 := dbtest.NewSession(t, d, db.StatusStarted)
	if _, err := sc.Run(ctx, session1, []string{root}); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	d.Writer.Flush()

	// Unplugged-drive scenario: the root vanishes entirely. The scan must fail
	// and the index must survive untouched
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	session2 := dbtest.NewSession(t, d, db.StatusStarted)
	if _, err := sc.Run(ctx, session2, []string{root}); err == nil {
		t.Fatal("scan of a missing root should fail")
	}
	d.Writer.Flush()

	rows := registryByName(t, d)
	if len(rows) != 1 {
		t.Fatalf("missing root swept the index: %d rows left, want 1", len(rows))
	}
	if rows["photo.jpg"].DeletedAt != nil {
		t.Error("missing root soft-deleted a file it never scanned")
	}
	if rows["photo.jpg"].SessionID != session1.String() {
		t.Errorf("surviving row reassigned to session %s", rows["photo.jpg"].SessionID)
	}
}

// TestRunPartialRootFailureSweepsOnlyCleanRoots: with two disjoint roots where
// one vanishes (unplugged drive), the surviving root still sweeps its own
// vanished files, the dead root's index stays untouched, and the scan fails
func TestRunPartialRootFailureSweepsOnlyCleanRoots(t *testing.T) {
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
	session1 := dbtest.NewSession(t, d, db.StatusStarted)
	if _, err := sc.Run(ctx, session1, []string{rootA, rootB}); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	d.Writer.Flush()

	if err := os.Remove(filepath.Join(rootA, "gone.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(rootB); err != nil {
		t.Fatal(err)
	}
	session2 := dbtest.NewSession(t, d, db.StatusStarted)
	if _, err := sc.Run(ctx, session2, []string{rootA, rootB}); err == nil {
		t.Fatal("scan with a missing root should fail")
	}
	d.Writer.Flush()

	rows := registryByName(t, d)
	if rows["gone.jpg"].DeletedAt == nil {
		t.Error("clean root's vanished file was not swept")
	}
	if rows["keep.jpg"].DeletedAt != nil {
		t.Error("clean root's live file was swept")
	}
	if rows["photo.jpg"].DeletedAt != nil {
		t.Error("failed root's file was swept despite the walk never running")
	}
	if rows["photo.jpg"].SessionID != session1.String() {
		t.Errorf("failed root's row reassigned to session %s", rows["photo.jpg"].SessionID)
	}
}

func TestRunPurgesExpiredRows(t *testing.T) {
	ctx := context.Background()
	sc, d := newDBScanner(t)

	// A file soft-deleted beyond the retention window, with dependent
	// metadata and vfs rows that must be purged in FK order
	old := dbtest.NewSession(t, d, db.StatusCompleted)
	dbtest.SeedFile(t, d, old, 1, "/gone", "expired.jpg", 10)
	if _, err := d.ExecContext(ctx,
		`UPDATE file_registry SET deleted_at = ? WHERE id = 1`,
		db.FormatTime(time.Now().Add(-deletedRetention-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO file_metadata (file_hash, file_id) VALUES ('expired-hash', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO virtual_fs_entries (session_id, file_id, source_path, target_path)
		VALUES (?, 1, '/gone/expired.jpg', 'stale/expired.jpg')`, old.String()); err != nil {
		t.Fatal(err)
	}
	// A file inside the retention window survives the purge
	dbtest.SeedFile(t, d, old, 2, "/gone", "recent.jpg", 10)
	if _, err := d.ExecContext(ctx,
		`UPDATE file_registry SET deleted_at = ? WHERE id = 2`,
		db.FormatTime(time.Now().Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}

	session := dbtest.NewSession(t, d, db.StatusStarted)
	if _, err := sc.Run(ctx, session, []string{t.TempDir()}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	d.Writer.Flush()

	rows := registryByName(t, d)
	if _, ok := rows["expired.jpg"]; ok {
		t.Error("expired soft-deleted row survived the purge")
	}
	if _, ok := rows["recent.jpg"]; !ok {
		t.Error("recently soft-deleted row was purged before its retention ran out")
	}
	var leftovers int
	if err := d.SQL.Get(&leftovers, `
		SELECT count(*) FROM file_metadata WHERE file_id = 1`); err != nil {
		t.Fatal(err)
	}
	if leftovers != 0 {
		t.Error("purged file left metadata rows behind")
	}
	if err := d.SQL.Get(&leftovers, `
		SELECT count(*) FROM virtual_fs_entries WHERE file_id = 1`); err != nil {
		t.Fatal(err)
	}
	if leftovers != 0 {
		t.Error("purged file left vfs rows behind")
	}
}
