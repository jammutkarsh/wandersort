// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

const (
	largeFileSizeBytes        int64 = 8 << 20  // 8 MiB keeps the normal unit test readable and fast
	constrainedMemoryLimit          = "64MiB"  // Child process heap target for resource-constrained tests
	fileFitsWithinMemoryBytes int64 = 16 << 20 // 16 MiB is below the configured memory limit
	fileExceedsMemoryBytes    int64 = 96 << 20 // 96 MiB is above the configured memory limit
	concurrentLargeFileCount        = 4
)

// missingExiftool is a path no binary lives at, so every extraction fails and
// the phase falls back to persisting the hash with empty metadata
func missingExiftool(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "missing-exiftool")
}

func status(t *testing.T, d *db.DB, id int64) string {
	t.Helper()
	var s string
	if err := d.SQL.Get(&s, `SELECT scan_status FROM file_registry WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	return s
}

// helperWritePatternFile writes a deterministic file of the requested size
// without holding the whole file in memory
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

	// Write the file in fixed-size chunks so test setup does not scale memory usage with file size
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

func TestHashFile(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"LargeFile", func(t *testing.T) {
			// 8 MiB is large enough to exercise the streaming path while remaining a fast
			// unit test. The resource-constrained cases below cover files larger than the
			// configured memory limit
			path := helperWritePatternFile(t, largeFileSizeBytes, 0x11)
			copyPath := helperWritePatternFile(t, largeFileSizeBytes, 0x11)
			mutatedPath := helperWritePatternFile(t, largeFileSizeBytes, 0x12)

			originalHash, err := hashFile(path)
			if err != nil {
				t.Fatalf("hashFile(%d bytes): %v", largeFileSizeBytes, err)
			}
			copyHash, err := hashFile(copyPath)
			if err != nil {
				t.Fatalf("hashFile(copy %d bytes): %v", largeFileSizeBytes, err)
			}
			mutatedHash, err := hashFile(mutatedPath)
			if err != nil {
				t.Fatalf("hashFile(mutated %d bytes): %v", largeFileSizeBytes, err)
			}

			if originalHash != copyHash {
				t.Errorf("identical %d-byte files should hash the same: %s vs %s", largeFileSizeBytes, originalHash, copyHash)
			}
			if originalHash == mutatedHash {
				t.Errorf("mutated %d-byte file should hash differently: %s", largeFileSizeBytes, originalHash)
			}
		}},
		{"ResourceConstrainedSingleFile", func(t *testing.T) {
			runHashingSubprocess(t, "TestHashFile_ResourceConstrainedSingleFileHelper")
		}},
		{"ConcurrentLargeFilesUnderMemoryLimit", func(t *testing.T) {
			runHashingSubprocess(t, "TestHashFile_ConcurrentLargeFilesUnderMemoryLimitHelper")
		}},
		{"WithRealTempDir", func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{
				"img1.jpg":  "JPEG content 1",
				"img2.jpg":  "JPEG content 1",
				"video.mp4": "MP4 content",
			}
			hashes := map[string]string{}

			for name, content := range files {
				p := filepath.Join(root, name)
				if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
				hash, err := hashFile(p)
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
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

func TestExtractor(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// A file exiftool cannot read is still a usable file: the pipeline knows
		// its hash and its folder, so the phase persists empty metadata, marks it
		// ANALYZED, and never fails the session
		{"RunToleratesExtractionFailure", func(t *testing.T) {
			ctx := context.Background()
			d := dbtest.New(t)

			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "photo.jpg"), []byte("bytes"), 0o644); err != nil {
				t.Fatal(err)
			}
			dbtest.SeedFile(t, d, 1, root, "photo.jpg", 5)

			e := New(d, logger.NewNoopLogger(), missingExiftool(t), 1)
			count, err := e.Run(ctx)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if count != 1 {
				t.Fatalf("Run read %d files, want 1", count)
			}
			d.Writer.Flush()

			if got := status(t, d, 1); got != db.StatusAnalyzed {
				t.Errorf("scan_status = %s, want %s", got, db.StatusAnalyzed)
			}
			// the hash is still persisted; only the exif columns stay NULL
			var rows int
			if err := d.SQL.Get(&rows,
				`SELECT count(*) FROM file_metadata
				 WHERE file_id = 1 AND file_hash != '' AND exif_make IS NULL`); err != nil {
				t.Fatal(err)
			}
			if rows != 1 {
				t.Errorf("failed extraction should leave one hashed row with NULL exif columns, got %d", rows)
			}
		}},
		// Sidecars (.AAE) carry no EXIF of their own, so running exiftool on them
		// is wasted work — they are still hashed and marked ANALYZED
		{"RunHashesSidecarsWithoutExiftool", func(t *testing.T) {
			ctx := context.Background()
			d := dbtest.New(t)

			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "IMG_0001.AAE"), []byte("plist"), 0o644); err != nil {
				t.Fatal(err)
			}
			dbtest.SeedFile(t, d, 1, root, "IMG_0001.AAE", 5)
			if _, err := d.ExecContext(ctx,
				`UPDATE file_registry SET media_type = ? WHERE id = 1`, classifier.MediaTypeSidecar); err != nil {
				t.Fatal(err)
			}

			e := New(d, logger.NewNoopLogger(), missingExiftool(t), 1)
			count, err := e.Run(ctx)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if count != 1 {
				t.Fatalf("Run read %d sidecars, want 1", count)
			}
			d.Writer.Flush()

			if got := status(t, d, 1); got != db.StatusAnalyzed {
				t.Errorf("sidecar scan_status = %s, want %s", got, db.StatusAnalyzed)
			}
			var hash string
			if err := d.SQL.Get(&hash, `SELECT file_hash FROM file_metadata WHERE file_id = 1`); err != nil {
				t.Fatal(err)
			}
			want, err := hashFile(filepath.Join(root, "IMG_0001.AAE"))
			if err != nil {
				t.Fatal(err)
			}
			if hash != want {
				t.Errorf("sidecar file_hash = %s, want %s", hash, want)
			}
		}},
		// A re-read file must end up with exactly one metadata row carrying the
		// current bytes' hash, not a second row next to the stale one
		{"RunReplacesStaleMetadata", func(t *testing.T) {
			ctx := context.Background()
			d := dbtest.New(t)

			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "photo.jpg"), []byte("current bytes"), 0o644); err != nil {
				t.Fatal(err)
			}

			dbtest.SeedFile(t, d, 1, root, "photo.jpg", 13)
			if _, err := d.ExecContext(ctx,
				`INSERT INTO file_metadata (file_hash, file_id, is_master) VALUES ('stale-hash', 1, 0)`); err != nil {
				t.Fatal(err)
			}

			e := New(d, logger.NewNoopLogger(), missingExiftool(t), 1)
			count, err := e.Run(ctx)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if count != 1 {
				t.Fatalf("Run read %d files, want 1", count)
			}
			d.Writer.Flush()

			var rows []struct {
				FileHash string `db:"file_hash"`
				IsMaster bool   `db:"is_master"`
			}
			if err := d.SQL.Select(&rows, `SELECT file_hash, is_master FROM file_metadata WHERE file_id = 1`); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("file has %d metadata rows after re-read, want exactly 1", len(rows))
			}
			wantHash, err := hashFile(filepath.Join(root, "photo.jpg"))
			if err != nil {
				t.Fatal(err)
			}
			if rows[0].FileHash != wantHash {
				t.Errorf("re-read metadata hash = %s, want %s", rows[0].FileHash, wantHash)
			}
			if !rows[0].IsMaster {
				t.Error("re-read metadata should return to the is_master default (1)")
			}
			if got := status(t, d, 1); got != db.StatusAnalyzed {
				t.Errorf("scan_status = %s, want %s", got, db.StatusAnalyzed)
			}
		}},
		// A file whose hash fails must not keep its previous metadata row — the
		// content is unknown, so the old hash would keep feeding the scorer and VFS
		{"RunHashFailureClearsStaleMetadata", func(t *testing.T) {
			ctx := context.Background()
			d := dbtest.New(t)

			// Registry points at a file that does not exist, so hashing fails
			dbtest.SeedFile(t, d, 1, t.TempDir(), "gone.jpg", 13)
			if _, err := d.ExecContext(ctx,
				`INSERT INTO file_metadata (file_hash, file_id) VALUES ('stale-hash', 1)`); err != nil {
				t.Fatal(err)
			}

			e := New(d, logger.NewNoopLogger(), missingExiftool(t), 1)
			if _, err := e.Run(ctx); err != nil {
				t.Fatalf("Run: %v", err)
			}
			d.Writer.Flush()

			var metadataRows int
			if err := d.SQL.Get(&metadataRows, `SELECT count(*) FROM file_metadata WHERE file_id = 1`); err != nil {
				t.Fatal(err)
			}
			if metadataRows != 0 {
				t.Errorf("failed hash left %d stale metadata rows, want 0", metadataRows)
			}
			if got := status(t, d, 1); got != db.StatusError {
				t.Errorf("scan_status = %s, want ERROR", got)
			}
		}},
		// The write itself: hash and extracted values land on one row
		{"StoreWritesHashAndExifTogether", func(t *testing.T) {
			d := dbtest.New(t)
			dbtest.SeedFile(t, d, 1, t.TempDir(), "photo.jpg", 13)

			e := New(d, logger.NewNoopLogger(), "exiftool", 1)
			if !d.Writer.Write(e.store(1, "hash-of-photo.jpg", classifier.CommonMetadata{
				ImageWidth:       "4032",
				ImageHeight:      "3024",
				Orientation:      "6",
				GPSLatitude:      "22.7196",
				GPSLongitude:     "75.8577",
				Make:             "Apple",
				Model:            "iPhone 13",
				DateTimeOriginal: "2024:03:05 11:22:33",
			})) {
				t.Fatal("bulk writer refused the metadata write")
			}
			d.Writer.Flush()

			var row struct {
				Hash        string   `db:"file_hash"`
				Width       *int     `db:"exif_image_width"`
				Lat         *float64 `db:"exif_gps_latitude"`
				Model       *string  `db:"exif_model"`
				Original    *string  `db:"exif_date_time_original"`
				CreateDate  *string  `db:"exif_create_date"`
				Orientation *int     `db:"exif_orientation"`
			}
			if err := d.SQL.Get(&row, `
				SELECT file_hash, exif_image_width, exif_gps_latitude, exif_model,
					exif_date_time_original, exif_create_date, exif_orientation
				FROM file_metadata WHERE file_id = 1`); err != nil {
				t.Fatal(err)
			}
			if row.Hash != "hash-of-photo.jpg" {
				t.Errorf("file_hash = %s, want the hash the worker computed", row.Hash)
			}
			if row.Width == nil || *row.Width != 4032 {
				t.Errorf("exif_image_width = %v, want 4032", row.Width)
			}
			if row.Orientation == nil || *row.Orientation != 6 {
				t.Errorf("exif_orientation = %v, want 6", row.Orientation)
			}
			if row.Lat == nil || *row.Lat != 22.7196 {
				t.Errorf("exif_gps_latitude = %v, want 22.7196", row.Lat)
			}
			if row.Model == nil || *row.Model != "iPhone 13" {
				t.Errorf("exif_model = %v, want iPhone 13", row.Model)
			}
			if row.Original == nil || *row.Original != "2024:03:05 11:22:33" {
				t.Errorf("exif_date_time_original = %v", row.Original)
			}
			// absent tags must stay NULL, not become ""
			if row.CreateDate != nil {
				t.Errorf("exif_create_date = %v, want NULL for an absent tag", *row.CreateDate)
			}
			if got := status(t, d, 1); got != db.StatusAnalyzed {
				t.Errorf("scan_status = %s, want %s", got, db.StatusAnalyzed)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

func TestHashFile_ResourceConstrainedSingleFileHelper(t *testing.T) {
	if os.Getenv("WANDERSORT_HASH_RESOURCE_HELPER") != "1" {
		t.Skip("helper subprocess")
	}

	underLimitPath := helperWritePatternFile(t, fileFitsWithinMemoryBytes, 0x21)
	overLimitPath := helperWritePatternFile(t, fileExceedsMemoryBytes, 0x22)

	underLimitHash, err := hashFile(underLimitPath)
	if err != nil {
		t.Fatalf("hashing %d-byte file below memory limit %s failed: %v", fileFitsWithinMemoryBytes, constrainedMemoryLimit, err)
	}
	overLimitHash, err := hashFile(overLimitPath)
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

func TestHashFile_ConcurrentLargeFilesUnderMemoryLimitHelper(t *testing.T) {
	if os.Getenv("WANDERSORT_HASH_RESOURCE_HELPER") != "1" {
		t.Skip("helper subprocess")
	}

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
			hash, err := hashFile(filePath)
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
