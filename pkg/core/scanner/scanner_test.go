package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/classifier"
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
