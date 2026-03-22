package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/core/classifier"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/util"
)

// ---------------------------------------------------------------------------
// PathUtil
// ---------------------------------------------------------------------------

func TestPathUtil_ExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home dir: %v", err)
	}

	pu := &util.Util{HomeDir: home}

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
	pu := &util.Util{HomeDir: "/home/testuser"}

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
			got := pu.ContractPath(tt.input)
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

	pu := &util.Util{HomeDir: root}

	rel, err := pu.MakeRelative(imgPath, photosRoot)
	if err != nil {
		t.Fatalf("MakeRelative: %v", err)
	}
	if rel != "2023/img.jpg" {
		t.Errorf("MakeRelative = %q, want %q", rel, "2023/img.jpg")
	}
}

func TestPathUtil_MakeAbsolute(t *testing.T) {
	pu := &util.Util{HomeDir: "/home/testuser"}
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
	pu := &util.Util{HomeDir: home}
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

// ---------------------------------------------------------------------------
// walkRoot — integration test with a real temp directory tree
// ---------------------------------------------------------------------------

// createTestTree builds a directory tree under t.TempDir() and returns the root.
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

func TestWalkRoot_DiscoverySmokeTest(t *testing.T) {
	root := createTestTree(t)
	log := logger.NewNoopLogger()
	fc := classifier.NewFileClassifier()

	pu := &util.Util{HomeDir: "/tmp"}
	sc := &Scanner{
		classifier: fc,
		log:        log,
		pathUtil:   pu,
		config: ScanConfig{
			MaxWalkers:       1,
			WorkerBufferSize: 100,
			BatchInsertSize:  50,
			ProgressInterval: time.Hour, // effectively disabled
		},
	}

	filesChan := make(chan FileDiscovery, 200)
	st := &scanState{tracker: &scanSessionTracker{}}

	err := sc.walkRoot(context.Background(), root, filesChan, st)
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

	// Verify counters
	discovered := st.tracker.discovered.Load()
	if discovered != 5 {
		t.Errorf("discovered = %d, want 5", discovered)
	}

	unsupported := st.tracker.unsupported
	if unsupported != 1 { // readme.txt
		t.Errorf("unsupported = %d, want 1", unsupported)
	}
}

func TestWalkRoot_SkipsIgnoredDirs(t *testing.T) {
	root := createTestTree(t)
	log := logger.NewNoopLogger()
	fc := classifier.NewFileClassifier()
	pu := &util.Util{HomeDir: "/tmp"}
	sc := &Scanner{classifier: fc, log: log, pathUtil: pu, config: ScanConfig{
		WorkerBufferSize: 100,
	}}

	filesChan := make(chan FileDiscovery, 200)
	st := &scanState{tracker: &scanSessionTracker{}}
	_ = sc.walkRoot(context.Background(), root, filesChan, st)
	close(filesChan)

	for d := range filesChan {
		if d.Path == ".git/HEAD" || d.Path == "HEAD" {
			t.Error("walkRoot should have skipped .git directory")
		}
	}
}

func TestWalkRoot_ContextCancellation(t *testing.T) {
	root := createTestTree(t)
	log := logger.NewNoopLogger()
	fc := classifier.NewFileClassifier()
	pu := &util.Util{HomeDir: "/tmp"}
	sc := &Scanner{classifier: fc, log: log, pathUtil: pu, config: ScanConfig{
		WorkerBufferSize: 100,
	}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	filesChan := make(chan FileDiscovery, 200)
	st := &scanState{tracker: &scanSessionTracker{}}
	err := sc.walkRoot(ctx, root, filesChan, st)
	close(filesChan)

	if err == nil {
		t.Error("walkRoot should return an error when context is cancelled")
	}
}

// ---------------------------------------------------------------------------
// FileDiscovery metadata fields
// ---------------------------------------------------------------------------

func TestFileDiscoveryCaptureStemAndRole(t *testing.T) {
	// Simulate what walkRoot produces: filename → DeriveCapture → FileDiscovery fields
	filename := "IMG_E3162.HEIC"
	ext := ".heic"
	mediaType := "IMAGE"
	capture := DeriveCapture(filename, ext, mediaType)

	fd := FileDiscovery{
		Path:        "Photos/IMG_E3162.HEIC",
		Size:        2048,
		Extension:   ext,
		MediaType:   mediaType,
		CaptureStem: capture.Stem,
		CaptureRole: capture.Role,
	}

	if fd.CaptureStem != "IMG_3162" {
		t.Errorf("CaptureStem = %q, want IMG_3162", fd.CaptureStem)
	}
	if fd.CaptureRole != CaptureRoleEdited {
		t.Errorf("CaptureRole = %q, want %q", fd.CaptureRole, CaptureRoleEdited)
	}
}

// ---------------------------------------------------------------------------
// FileRegistry model
// ---------------------------------------------------------------------------

func TestFileRegistry_GetAbsolutePath(t *testing.T) {
	pu := &util.Util{HomeDir: "/home/user"}

	tests := []struct {
		name string
		fr   FileRegistry
		want string
	}{
		{
			"absolute",
			FileRegistry{FilePath: "/data/photos/img.jpg", PathType: PathTypeAbsolute},
			"/data/photos/img.jpg",
		},
		{
			"relative",
			FileRegistry{FilePath: "2023/img.jpg", SourceRoot: "~/Photos", PathType: PathTypeRelative},
			"/home/user/Photos/2023/img.jpg",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fr.GetAbsolutePath(pu)
			if got != tt.want {
				t.Errorf("GetAbsolutePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFileRegistry_IsPrimarySource(t *testing.T) {
	tests := []struct {
		mediaType string
		want      bool
	}{
		{classifier.MediaTypeImage, true},
		{classifier.MediaTypeRaw, true},
		{classifier.MediaTypeVideo, true},
		{classifier.MediaTypeSidecar, false},
		{classifier.MediaTypeUnknown, false},
	}
	for _, tt := range tests {
		fr := FileRegistry{MediaType: tt.mediaType}
		if got := fr.IsPrimarySource(); got != tt.want {
			t.Errorf("IsPrimarySource() for %q = %v, want %v", tt.mediaType, got, tt.want)
		}
	}
}

func TestFileRegistry_NeedsTranscoding(t *testing.T) {
	raw := FileRegistry{MediaType: classifier.MediaTypeRaw}
	if !raw.NeedsTranscoding() {
		t.Error("RAW file should need transcoding")
	}
	img := FileRegistry{MediaType: classifier.MediaTypeImage}
	if img.NeedsTranscoding() {
		t.Error("IMAGE file should not need transcoding")
	}
}

// ---------------------------------------------------------------------------
// Concurrent walkRoot — multiple goroutines walking same tree
// ---------------------------------------------------------------------------

func TestWalkRoot_ConcurrentWalkers(t *testing.T) {
	root := createTestTree(t)
	log := logger.NewNoopLogger()
	fc := classifier.NewFileClassifier()
	pu := &util.Util{HomeDir: "/tmp"}
	sc := &Scanner{classifier: fc, log: log, pathUtil: pu, config: ScanConfig{
		WorkerBufferSize: 500,
	}}

	const walkers = 4
	filesChan := make(chan FileDiscovery, 1000)
	st := &scanState{tracker: &scanSessionTracker{}}

	var wg sync.WaitGroup
	for range walkers {
		wg.Go(func() {
			_ = sc.walkRoot(context.Background(), root, filesChan, st)
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

	// Atomic counter should also match
	discovered := st.tracker.discovered.Load()
	if discovered != int64(expected) {
		t.Errorf("discovered counter = %d, want %d", discovered, expected)
	}
}
