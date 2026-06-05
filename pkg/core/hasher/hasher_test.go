package hasher

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

const (
	largeFileSizeBytes        int64 = 8 << 20  // 8 MiB keeps the normal unit test readable and fast.
	constrainedMemoryLimit          = "64MiB"  // Child process heap target for resource-constrained tests.
	fileFitsWithinMemoryBytes int64 = 16 << 20 // 16 MiB is below the configured memory limit.
	fileExceedsMemoryBytes    int64 = 96 << 20 // 96 MiB is above the configured memory limit.
	concurrentLargeFileCount        = 4
)

// helperHasher returns a *Hasher with nil DB (HashFile doesn't touch DB).
func helperHasher() *Hasher {
	return &Hasher{}
}

// helperWritePatternFile writes a deterministic file of the requested size
// without holding the whole file in memory.
func helperWritePatternFile(t *testing.T, size int64, seed byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "hash-*")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	const chunkSize = 256 << 10 // 256 KiB
	chunk := make([]byte, chunkSize)
	for i := range chunk {
		chunk[i] = seed + byte(i%251)
	}

	// Write the file in fixed-size chunks so test setup does not scale memory usage with file size.
	remaining := size
	for remaining > 0 {
		writeSize := len(chunk)
		if remaining < int64(writeSize) {
			writeSize = int(remaining)
		}
		if _, err := f.Write(chunk[:writeSize]); err != nil {
			t.Fatal(err)
		}
		remaining -= int64(writeSize)
	}

	return f.Name()
}

func runHashingSubprocess(t *testing.T, testName string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$", "-test.v")
	cmd.Env = append(
		os.Environ(),
		"WANDERSORT_HASH_RESOURCE_HELPER=1",
		"GOMEMLIMIT="+constrainedMemoryLimit,
		"GOMAXPROCS=1",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed under constrained resources: %v\n%s", testName, err, output)
	}
}

func TestHashFile_LargeFile(t *testing.T) {
	// 8 MiB is large enough to exercise the streaming path while remaining a fast
	// unit test. The resource-constrained cases below cover files larger than the
	// configured memory limit.
	path := helperWritePatternFile(t, largeFileSizeBytes, 0x11)
	copyPath := helperWritePatternFile(t, largeFileSizeBytes, 0x11)
	mutatedPath := helperWritePatternFile(t, largeFileSizeBytes, 0x12)
	h := helperHasher()

	originalHash, err := h.hashFile(path)
	if err != nil {
		t.Fatalf("HashFile(%d bytes): %v", largeFileSizeBytes, err)
	}
	copyHash, err := h.hashFile(copyPath)
	if err != nil {
		t.Fatalf("HashFile(copy %d bytes): %v", largeFileSizeBytes, err)
	}
	mutatedHash, err := h.hashFile(mutatedPath)
	if err != nil {
		t.Fatalf("HashFile(mutated %d bytes): %v", largeFileSizeBytes, err)
	}

	if originalHash != copyHash {
		t.Errorf("identical %d-byte files should hash the same: %s vs %s", largeFileSizeBytes, originalHash, copyHash)
	}
	if originalHash == mutatedHash {
		t.Errorf("mutated %d-byte file should hash differently: %s", largeFileSizeBytes, originalHash)
	}
}

func TestHashFile_ResourceConstrainedSingleFile(t *testing.T) {
	runHashingSubprocess(t, "TestHashFile_ResourceConstrainedSingleFileHelper")
}

func TestHashFile_ResourceConstrainedSingleFileHelper(t *testing.T) {
	if os.Getenv("WANDERSORT_HASH_RESOURCE_HELPER") != "1" {
		t.Skip("helper subprocess")
	}

	h := helperHasher()
	underLimitPath := helperWritePatternFile(t, fileFitsWithinMemoryBytes, 0x21)
	overLimitPath := helperWritePatternFile(t, fileExceedsMemoryBytes, 0x22)

	underLimitHash, err := h.hashFile(underLimitPath)
	if err != nil {
		t.Fatalf("hashing %d-byte file below memory limit %s failed: %v", fileFitsWithinMemoryBytes, constrainedMemoryLimit, err)
	}
	overLimitHash, err := h.hashFile(overLimitPath)
	if err != nil {
		t.Fatalf("hashing %d-byte file above memory limit %s failed: %v", fileExceedsMemoryBytes, constrainedMemoryLimit, err)
	}

	if underLimitHash == "" || overLimitHash == "" {
		t.Fatal("hashes should not be empty under constrained resources")
	}
	if underLimitHash == overLimitHash {
		t.Fatalf("files with different sizes and content should not hash the same under constrained resources: %s", underLimitHash)
	}
}

// ---------------------------------------------------------------------------
// Concurrent hashing
// ---------------------------------------------------------------------------

func TestHashFile_ConcurrentLargeFilesUnderMemoryLimit(t *testing.T) {
	runHashingSubprocess(t, "TestHashFile_ConcurrentLargeFilesUnderMemoryLimitHelper")
}

func TestHashFile_ConcurrentLargeFilesUnderMemoryLimitHelper(t *testing.T) {
	if os.Getenv("WANDERSORT_HASH_RESOURCE_HELPER") != "1" {
		t.Skip("helper subprocess")
	}

	h := helperHasher()
	paths := make([]string, concurrentLargeFileCount)
	for i := range concurrentLargeFileCount {
		paths[i] = helperWritePatternFile(t, fileExceedsMemoryBytes, byte(0x30+i))
	}

	type result struct {
		index int
		hash  string
		err   error
	}

	results := make(chan result, concurrentLargeFileCount)
	var wg sync.WaitGroup
	for i, path := range paths {
		wg.Add(1)
		go func(index int, filePath string) {
			defer wg.Done()
			hash, err := h.hashFile(filePath)
			results <- result{index: index, hash: hash, err: err}
		}(i, path)
	}
	wg.Wait()
	close(results)

	seen := make(map[string]int, concurrentLargeFileCount)
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent hash for file %d (%d bytes, memory limit %s) failed: %v", result.index, fileExceedsMemoryBytes, constrainedMemoryLimit, result.err)
		}
		if result.hash == "" {
			t.Fatalf("concurrent hash for file %d was empty", result.index)
		}
		if previous, exists := seen[result.hash]; exists {
			t.Fatalf("concurrent hashes collided for files %d and %d: %s", previous, result.index, result.hash)
		}
		seen[result.hash] = result.index
	}

	if len(seen) != concurrentLargeFileCount {
		t.Fatalf("expected %d successful concurrent hashes, got %d", concurrentLargeFileCount, len(seen))
	}
}

// ---------------------------------------------------------------------------
// Directory-level hashing behavior
// ---------------------------------------------------------------------------

func TestHashFile_WithRealTempDir(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"img1.jpg":  "JPEG content 1",
		"img2.jpg":  "JPEG content 1",
		"video.mp4": "MP4 content",
	}
	h := helperHasher()
	hashes := map[string]string{}

	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		hash, err := h.hashFile(p)
		if err != nil {
			t.Fatal(err)
		}
		hashes[name] = hash
	}

	if hashes["img1.jpg"] != hashes["img2.jpg"] {
		t.Errorf("files with the same content should hash the same: %q vs %q", hashes["img1.jpg"], hashes["img2.jpg"])
	}
	if hashes["img1.jpg"] == hashes["video.mp4"] {
		t.Errorf("files with different content should hash differently: %q", hashes["img1.jpg"])
	}
}
