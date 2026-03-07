package hasher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"lukechampine.com/blake3"
)

// helperHasher returns a *Hasher with nil DB (HashFile doesn't touch DB).
func helperHasher() *Hasher {
	return &Hasher{}
}

// helperWriteFile creates a temp file with content and returns its path.
func helperWriteFile(t *testing.T, content []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "hash-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

// referenceBlake3 computes BLAKE3(data) the same way as HashFile for comparison.
func referenceBlake3(data []byte) string {
	h := blake3.New(32, nil)
	h.Write(data)
	sum := make([]byte, 0, 32)
	return hex.EncodeToString(h.Sum(sum))
}

func TestHashFile_KnownContent(t *testing.T) {
	content := []byte("hello, wandersort!")
	path := helperWriteFile(t, content)

	got, err := helperHasher().HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}

	want := referenceBlake3(content)
	if got != want {
		t.Errorf("HashFile = %q, want %q", got, want)
	}
}

func TestHashFile_EmptyFile(t *testing.T) {
	path := helperWriteFile(t, nil)

	got, err := helperHasher().HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}

	want := referenceBlake3(nil)
	if got != want {
		t.Errorf("HashFile(empty) = %q, want %q", got, want)
	}
}

func TestHashFile_Is64HexChars(t *testing.T) {
	path := helperWriteFile(t, []byte("test"))
	hash, err := helperHasher().HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64", len(hash))
	}
	if _, err := hex.DecodeString(hash); err != nil {
		t.Errorf("hash is not valid hex: %v", err)
	}
}

func TestHashFile_IdenticalContentSameHash(t *testing.T) {
	data := []byte("duplicate content")
	p1 := helperWriteFile(t, data)
	p2 := helperWriteFile(t, data)

	h := helperHasher()
	h1, err := h.HashFile(p1)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := h.HashFile(p2)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("identical files got different hashes: %s vs %s", h1, h2)
	}
}

func TestHashFile_DifferentContentDifferentHash(t *testing.T) {
	p1 := helperWriteFile(t, []byte("file A"))
	p2 := helperWriteFile(t, []byte("file B"))

	h := helperHasher()
	h1, _ := h.HashFile(p1)
	h2, _ := h.HashFile(p2)
	if h1 == h2 {
		t.Error("different files should have different hashes")
	}
}

func TestHashFile_NonexistentFile(t *testing.T) {
	_, err := helperHasher().HashFile("/nonexistent/path/to/file")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestHashFile_LargeFile(t *testing.T) {
	data := make([]byte, 1<<20)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	path := helperWriteFile(t, data)

	got, err := helperHasher().HashFile(path)
	if err != nil {
		t.Fatalf("HashFile(1MB): %v", err)
	}

	want := referenceBlake3(data)
	if got != want {
		t.Errorf("HashFile(1MB) mismatch: got %s, want %s", got, want)
	}
}

// ---------------------------------------------------------------------------
// Concurrent hashing
// ---------------------------------------------------------------------------

func TestHashFile_ConcurrentSameFile(t *testing.T) {
	data := []byte("concurrent content")
	path := helperWriteFile(t, data)
	h := helperHasher()
	want := referenceBlake3(data)

	const goroutines = 50
	errs := make(chan error, goroutines)
	hashes := make(chan string, goroutines)

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			hash, err := h.HashFile(path)
			errs <- err
			hashes <- hash
		})
	}
	wg.Wait()
	close(errs)
	close(hashes)

	for err := range errs {
		if err != nil {
			t.Fatalf("HashFile concurrent error: %v", err)
		}
	}
	for hash := range hashes {
		if hash != want {
			t.Errorf("concurrent hash mismatch: %s vs %s", hash, want)
		}
	}
}

func TestHashFile_ConcurrentDifferentFiles(t *testing.T) {
	const n = 20
	type entry struct {
		path string
		want string
	}
	entries := make([]entry, n)
	for i := range n {
		data := []byte(strings.Repeat("X", (i+1)*100))
		entries[i] = entry{
			path: helperWriteFile(t, data),
			want: referenceBlake3(data),
		}
	}

	h := helperHasher()
	results := make([]string, n)
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Add(1)
		go func(idx int, p string) {
			defer wg.Done()
			hash, err := h.HashFile(p)
			if err != nil {
				t.Errorf("HashFile[%d]: %v", idx, err)
				return
			}
			results[idx] = hash
		}(i, e.path)
	}
	wg.Wait()

	for i, e := range entries {
		if results[i] != e.want {
			t.Errorf("file %d: got %s, want %s", i, results[i], e.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Scorer patterns and lookup
// ---------------------------------------------------------------------------

func TestDatePatterns(t *testing.T) {
	cases := []struct {
		input string
		match bool
	}{
		{"20230520_trip.jpg", true},
		{"2023-05-20_photo.jpg", true},
		{"2023_05_20_sunset.jpg", true},
		{"IMG_3162.HEIC", false},
		{"no_date.jpg", false},
	}
	for _, tt := range cases {
		t.Run(tt.input, func(t *testing.T) {
			matched := false
			for _, re := range datePatterns {
				if re.MatchString(tt.input) {
					matched = true
					break
				}
			}
			if matched != tt.match {
				t.Errorf("datePatterns match(%q) = %v, want %v", tt.input, matched, tt.match)
			}
		})
	}
}

func TestGenericDirNames(t *testing.T) {
	for _, name := range []string{"dcim", "camera", "photos", "downloads", "temp"} {
		if !genericDirNames[name] {
			t.Errorf("expected %q to be a generic dir name", name)
		}
	}
	for _, name := range []string{"vacation2023", "wedding", "project"} {
		if genericDirNames[name] {
			t.Errorf("expected %q to NOT be a generic dir name", name)
		}
	}
}

func TestContentGroupModel(t *testing.T) {
	g := ContentGroup{ID: 1, ContentHash: "abc123", TotalCopies: 3}
	if g.ContentHash != "abc123" {
		t.Error("field mismatch")
	}
	if g.MasterFileID != nil {
		t.Error("MasterFileID should be nil")
	}
}

func TestScoringCriteria(t *testing.T) {
	sc := ScoringCriteria{HasEXIF: true, HasDatePattern: true, HasMeaningfulDir: false}
	if !sc.HasEXIF || !sc.HasDatePattern || sc.HasMeaningfulDir {
		t.Error("ScoringCriteria fields mismatch")
	}
}

func TestScorer_CalculateScore_Stub(t *testing.T) {
	s := NewScorer(nil, nil)
	score, err := s.CalculateScore(context.Background(), 42)
	if err != nil {
		t.Fatalf("CalculateScore: %v", err)
	}
	if score != 0 {
		t.Errorf("stub scorer should return 0, got %d", score)
	}
}

// ---------------------------------------------------------------------------
// End-to-end: hash a temp directory of files
// ---------------------------------------------------------------------------

func TestHashFile_WithRealTempDir(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"img1.jpg":  "JPEG content 1",
		"img2.jpg":  "JPEG content 2",
		"video.mp4": "MP4 content",
	}
	h := helperHasher()
	hashes := map[string]string{}

	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		hash, err := h.HashFile(p)
		if err != nil {
			t.Fatal(err)
		}
		hashes[name] = hash
	}

	seen := map[string]string{}
	for name, hash := range hashes {
		if prev, ok := seen[hash]; ok {
			t.Errorf("duplicate hash %q between %s and %s", hash, prev, name)
		}
		seen[hash] = name
	}
}
