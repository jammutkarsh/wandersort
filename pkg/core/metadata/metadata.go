// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package metadata is the pipeline's single read pass over every discovered
// file: one worker hashes the bytes and then runs exiftool over the same file,
// back to back, so the header read exiftool needs hits the page cache the hash
// just warmed. Hashing and EXIF used to be two phases, which meant reading
// every file twice — on a library big enough to matter, far enough apart that
// the cache had already evicted it, so the second pass paid full disk cost.
package metadata

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/jmoiron/sqlx"
	"golang.org/x/sync/semaphore"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/volume"
	"lukechampine.com/blake3"
)

// hashOutputSize is the output length of BLAKE3-256 in bytes
const hashOutputSize = 32

// maxReadBudget caps concurrent byte reads regardless of how many cores the
// machine has. NVMe queues are deep, but random-read IOPS plateau somewhere
// around 16-32 outstanding requests — past that it is the same throughput at
// worse latency. The cap is on reads only: exiftool is a CPU-bound Perl
// process, and a 64-core box genuinely wants 64 of those.
const maxReadBudget = 16

// fileRecord is what the claim query hands a worker: mediaType is carried so a
// sidecar can be hashed without paying for an exiftool call it has no tags
// for, and cost is what reading it charges the shared read budget
type fileRecord struct {
	id        int64
	absPath   string
	mediaType string
	cost      int64
	// sizeUnique means no other live file is this many bytes long, so nothing
	// can share its content and the read would decide nothing
	sizeUnique bool
}

// Extractor hashes discovered files and reads their EXIF in one pass
type Extractor struct {
	db      *db.DB
	log     logger.Logger
	pool    *exiftool.Pool
	workers int

	// reads is admission control over the one thing the storage actually
	// limits: bytes coming off the platter. Same idea as Postgres' per-device
	// random_page_cost — a spinning disk charges the whole budget for one
	// file, an SSD charges 1 — except it throttles rather than plans.
	//
	// ponytail: one shared budget couples physically independent devices, so
	// an HDD read in flight also throttles an idle SSD. Only reachable at a
	// volume boundary, since the producer drains one volume at a time;
	// per-volume budgets are the upgrade path if a mixed library measures
	// badly.
	reads  *semaphore.Weighted
	budget int64
	// classes caches the storage class per volume UUID. Producer-only, so it
	// needs no lock.
	classes map[string]volume.Class
	// uniqueSize holds every byte length that occurs exactly once across the
	// live library. Filled before the producer starts and read-only after, so
	// it needs no lock either.
	uniqueSize map[int64]struct{}
}

func New(database *db.DB, log logger.Logger, exiftoolPath string, workers int) *Extractor {
	pool, err := exiftool.NewPool(exiftoolPath, workers)
	if err != nil {
		// An unavailable exiftool binary is not fatal: files still get hashed
		// and persisted with empty metadata so the pipeline can proceed.
		log.Warn("Exiftool unavailable; metadata will be empty", "error", err)
	}

	budget := int64(min(max(workers, 1), maxReadBudget))
	return &Extractor{
		db:      database,
		log:     log,
		pool:    pool,
		workers: workers,
		reads:   semaphore.NewWeighted(budget),
		budget:  budget,
		classes: map[string]volume.Class{},
	}
}

// sizeDerivedHash stands in for the content hash of a file that was never
// read. **It must be unique per file**: the scorer groups duplicates purely by
// file_hash, so a shared sentinel would report every unread file as a copy of
// every other one
func sizeDerivedHash(fileID int64) string {
	return fmt.Sprintf("size:%d", fileID)
}

// readTargets is how many files of a class may be read at once when the budget
// is fully available. The class flips the sign of the concurrency term rather
// than scaling it: on a spinning disk, eight readers drag the head across the
// platter between reads and the interleave costs far more than the seeks it
// was meant to overlap.
var readTargets = map[volume.Class]int64{
	volume.ClassRotational: 1, // seek interleave is the whole cost
	volume.ClassRemovable:  2, // flash controllers stall on deep queues
	volume.ClassUnknown:    4, // conservative, never a guess
	// SolidState and Network take the whole budget: one has no seek penalty,
	// the other is latency-bound, so in-flight requests hide round trips.
}

// readCost charges a file of this class against a budget of B. Clamped to
// [1, budget] — a cost above the budget would block forever
func readCost(class volume.Class, budget int64) int64 {
	target, ok := readTargets[class]
	if !ok || target >= budget {
		return 1
	}
	cost := (budget + target - 1) / target // ceil
	return min(max(cost, 1), budget)
}

// Run claims every DISCOVERED file one at a time and reads it in a bounded
// worker pool. Returns how many files were persisted
func (e *Extractor) Run(ctx context.Context) (int, error) {
	if e.pool != nil {
		defer e.pool.Close()
	}

	toRead := make(chan fileRecord, 2*e.workers)
	producerErr := make(chan error, 1)

	var extracted atomic.Int64

	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	e.log.Info("Extracting metadata")

	// before anything is counted or claimed: a file skipped as size-unique by
	// an earlier run may not be size-unique any more
	if err := e.rehashOutdatedSizeHashes(ctx); err != nil {
		return 0, err
	}

	sizes, err := e.uniqueSizes(ctx)
	if err != nil {
		return 0, err
	}
	// written before the producer goroutine starts and read-only after
	e.uniqueSize = sizes

	var total int
	if err := e.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM live_files WHERE scan_status = ?
	`, db.StatusDiscovered).Scan(&total); err != nil {
		e.log.Warn("Failed to count files to read", "error", err)
	}

	go e.producer(ctxWithCancel, cancel, toRead, producerErr)

	// Workers write straight through the BulkWriter rather than funnelling into
	// a store goroutine: the writer already serializes every operation, so the
	// extra hop would only add a channel
	var wg sync.WaitGroup
	for range e.workers {
		wg.Go(func() {
			e.worker(ctxWithCancel, toRead, &extracted, total)
		})
	}
	wg.Wait()

	if err := <-producerErr; err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	persisted := int(extracted.Load())
	e.log.Info("Metadata extraction complete", "filesRead", persisted)
	return persisted, nil
}

// producer drains one volume at a time, fastest first, and feeds the workers.
// Ordering by device does not change total wall time — the same bytes are read
// either way — but it front-loads progress, so an interrupted run has the cheap
// files done and the progress bar moves early instead of crawling behind an
// HDD. Within a volume the claim order is untouched: id is discovery order is
// walk order is roughly directory order, which is as seek-friendly as a claim
// order gets.
func (e *Extractor) producer(ctx context.Context, cancel context.CancelFunc, toRead chan<- fileRecord, producerErr chan<- error) {
	defer close(toRead)

	fail := func(err error) {
		producerErr <- err
		cancel()
	}

	volumes, err := e.pendingVolumes(ctx)
	if err != nil {
		fail(err)
		return
	}

	// The final pass is unscoped: it catches anything the grouping missed, so
	// no row can be stranded by an edge case in the query above.
	for _, v := range append(volumes, pendingVolume{all: true}) {
		for {
			record, ok, err := e.getFile(ctx, v)
			if err != nil {
				fail(err)
				return
			}
			if !ok {
				break // this volume is drained; move to the next
			}

			select {
			case toRead <- record:
			case <-ctx.Done():
				producerErr <- ctx.Err()
				return
			}
		}
	}
	producerErr <- nil
}

// uniqueSizes returns every byte length that occurs exactly once across the
// live library. A file of such a length cannot share content with anything,
// so reading it decides nothing the size did not already decide — that is the
// whole prefilter. Measured on a 107k-file library: 15,246 files, 187.5 GiB,
// 23.9% of the bytes, never read.
//
// Computed once because nothing changes it mid-phase: the scan phase has
// finished, and claiming a file only moves its status
func (e *Extractor) uniqueSizes(ctx context.Context) (map[int64]struct{}, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT file_size FROM live_files GROUP BY file_size HAVING COUNT(*) = 1
	`)
	if err != nil {
		return nil, fmt.Errorf("find unique file sizes: %w", err)
	}
	defer rows.Close()

	sizes := map[int64]struct{}{}
	for rows.Next() {
		var size int64
		if err := rows.Scan(&size); err != nil {
			return nil, fmt.Errorf("scan unique file size: %w", err)
		}
		sizes[size] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find unique file sizes: %w", err)
	}
	return sizes, nil
}

// rehashOutdatedSizeHashes sends back for a real read every file that was
// skipped as size-unique and is not size-unique any more.
//
// **Do not remove this, and do not move it after the claim.** Tier 1's claim —
// "nothing else in the library is this long, so nothing can share its
// content" — is only true of the library as it stood when the file was
// skipped. A later scan adding a same-size file makes it false, and the
// failure mode is silent: two identical files reported as distinct, which is
// worse than the read it saved. The newcomer is DISCOVERED already; this is
// what fetches the incumbent back
func (e *Extractor) rehashOutdatedSizeHashes(ctx context.Context) error {
	res, err := e.db.ExecContext(ctx, `
		UPDATE file_registry
		SET scan_status = ?
		WHERE id IN (
			SELECT m.file_id
			FROM file_metadata m
			JOIN live_files f ON f.id = m.file_id
			WHERE m.hash_kind = ?
			  AND f.file_size IN (
				SELECT file_size FROM live_files GROUP BY file_size HAVING COUNT(*) > 1
			  )
		)`, db.StatusDiscovered, db.HashSize)
	if err != nil {
		return fmt.Errorf("re-queue outdated size-derived hashes: %w", err)
	}
	// the stale metadata row is left alone: store() deletes it before writing
	// the real one, the same way it handles any other re-read
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		e.log.Info("Re-reading files that are no longer a unique size", logger.UserKey, true,
			"files", n)
	}
	return nil
}

// pendingVolume is one volume's share of the work still to read. all is the
// closing sweep, and is not the same thing as an empty uuid — files whose
// volume never resolved are a real group of their own, and scoping to them
// with "no filter" would drain every other volume out of order
type pendingVolume struct {
	uuid string
	cost int64
	all  bool
}

// pendingVolumes groups the unread files by volume and prices each one. The
// scan phase has already finished by the time this runs, so no new volume can
// appear underneath it
func (e *Extractor) pendingVolumes(ctx context.Context) ([]pendingVolume, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT COALESCE(volume_uuid, ''), MIN(file_dir)
		FROM live_files
		WHERE scan_status = ?
		GROUP BY COALESCE(volume_uuid, '')
	`, db.StatusDiscovered)
	if err != nil {
		return nil, fmt.Errorf("group pending files by volume: %w", err)
	}
	defer rows.Close()

	var volumes []pendingVolume
	for rows.Next() {
		var uuid, sampleDir string
		if err := rows.Scan(&uuid, &sampleDir); err != nil {
			return nil, fmt.Errorf("scan pending volume: %w", err)
		}
		class := e.classOf(uuid, sampleDir)
		cost := readCost(class, e.budget)
		e.log.Info("Storage detected", logger.UserKey, true,
			"path", sampleDir, "class", class.String(), "concurrentReads", e.budget/cost)
		volumes = append(volumes, pendingVolume{uuid: uuid, cost: cost})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("group pending files by volume: %w", err)
	}

	// cheapest cost first == fastest device first; uuid breaks ties so the
	// order is deterministic run to run
	sort.Slice(volumes, func(i, j int) bool {
		if volumes[i].cost != volumes[j].cost {
			return volumes[i].cost < volumes[j].cost
		}
		return volumes[i].uuid < volumes[j].uuid
	})
	return volumes, nil
}

// classOf resolves and caches a volume's storage class. An unresolved UUID is
// not worth a lookup: the same platform machinery produces both, so if the
// UUID failed the class would too — and keying the cache on the directory
// instead would spawn a resolution per directory
func (e *Extractor) classOf(uuid, sampleDir string) volume.Class {
	if uuid == "" {
		return volume.ClassUnknown
	}
	if class, ok := e.classes[uuid]; ok {
		return class
	}
	class := volume.ClassForPath(sampleDir)
	e.classes[uuid] = class
	return class
}

// getFile atomically claims the next discovered file on one volume, or on any
// volume during the closing sweep. The ANALYZING stamp is what makes an
// interrupted phase resumable: the scanner's upsert resets those rows to
// DISCOVERED, since nothing was persisted for them
func (e *Extractor) getFile(ctx context.Context, v pendingVolume) (fileRecord, bool, error) {
	var id, fileSize int64
	var fileDir, fileName, mediaType, uuid string
	query := `
	UPDATE file_registry
	SET scan_status = ?
	WHERE id = (
		SELECT id
		FROM live_files
		WHERE scan_status = ?
		  AND (? OR COALESCE(volume_uuid, '') = ?)
		ORDER BY id
		LIMIT 1
	)
	RETURNING id, file_dir, file_name, COALESCE(media_type, ''), COALESCE(volume_uuid, ''), file_size`

	err := e.db.
		QueryRowContext(ctx, query, db.StatusAnalyzing, db.StatusDiscovered, v.all, v.uuid).
		Scan(&id, &fileDir, &fileName, &mediaType, &uuid, &fileSize)
	if errors.Is(err, sql.ErrNoRows) {
		return fileRecord{}, false, nil
	}
	if err != nil {
		return fileRecord{}, false, fmt.Errorf("claim next metadata row: %w", err)
	}

	cost := v.cost
	if v.all {
		// the closing sweep has no price of its own: charge the straggler by
		// whatever volume it turned out to be on
		cost = readCost(e.classOf(uuid, fileDir), e.budget)
	}
	_, sizeUnique := e.uniqueSize[fileSize]
	return fileRecord{
		id:         id,
		absPath:    filepath.Join(fileDir, fileName),
		mediaType:  mediaType,
		cost:       cost,
		sizeUnique: sizeUnique,
	}, true, nil
}

// worker hashes one file, reads its EXIF while the bytes are still cached, and
// enqueues the single write that persists both
func (e *Extractor) worker(ctx context.Context, toRead <-chan fileRecord, extracted *atomic.Int64, total int) {
	for file := range toRead {
		if ctx.Err() != nil {
			return
		}

		// Nothing in the library is this file's length, so nothing can share
		// its bytes: the read would confirm a duplicate group of one. exiftool
		// still runs below — the folder proposal needs the tags either way.
		hash, hashKind := sizeDerivedHash(file.id), db.HashSize
		if !file.sizeUnique {
			var err error
			if hash, err = e.readFile(ctx, file); err != nil {
				// A cancelled pipeline aborts the budget wait rather than the
				// read; that is shutdown, not a bad file, so don't park it at
				// ERROR on the way out.
				if ctx.Err() != nil {
					return
				}
				e.log.Error("Failed to hash file", "fileId", file.id, "path", file.absPath, "error", err)
				e.db.Writer.Write(storeFailure(file.id))
				continue
			}
			hashKind = db.HashContent
		}

		// A failed extraction is not a failed file: the pipeline still knows the
		// file's hash and its folder context, so the VFS can place it. Persist
		// the empty metadata and move on
		var meta classifier.CommonMetadata
		// Sidecars (iPhone .AAE edit files) carry no EXIF of their own, so
		// spawning exiftool on them is pure waste — hash them and move on
		if file.mediaType != classifier.MediaTypeSidecar {
			var err error
			if e.pool != nil {
				meta, err = e.pool.Extract(ctx, file.absPath)
			} else {
				err = fmt.Errorf("exiftool not available")
			}
			if err != nil {
				// A cancelled pipeline SIGKILLs the exiftool child ("signal:
				// killed") and fails the next call with "context canceled" —
				// that is shutdown, not a bad file, so don't report it as an
				// extraction failure.
				if ctx.Err() != nil {
					return
				}
				e.log.Warn("Failed to extract exif data", "fileId", file.id, "path", file.absPath, "error", err)
			}
		}

		// StreamKey: feeds the TUI progress bar, stripped from the plain console.
		e.log.Info("Reading", logger.StreamKey, true,
			"file", filepath.Base(file.absPath), "extracted", extracted.Add(1), "total", total)

		if !e.db.Writer.Write(e.store(file.id, hash, hashKind, meta)) {
			e.log.Warn("Bulk writer closed; dropping metadata write", "fileId", file.id)
			return
		}
	}
}

// readFile gates the byte read on the storage's weighted budget. exiftool is
// deliberately left outside the gate: it reads a header the hash just warmed
// in the page cache, and it is a CPU-bound Perl process rather than a seek —
// throttling it to the disk's concurrency would trade the hash win straight
// back for an exif loss
func (e *Extractor) readFile(ctx context.Context, file fileRecord) (string, error) {
	if err := e.reads.Acquire(ctx, file.cost); err != nil {
		return "", err // cancelled
	}
	defer e.reads.Release(file.cost)
	return hashFile(file.absPath)
}

// hashBufferSize is how much of a file is pulled per read syscall. io.Copy's
// default is 32 KiB, which on a 783 GiB library is ~25.6 million syscalls and
// gives the kernel a small window to read ahead into. 1 MiB is 32× fewer,
// and small enough that a full pool of them is a rounding error next to the
// exiftool processes running beside it.
const hashBufferSize = 1 << 20

// hashBuffers keeps one buffer per in-flight read alive rather than
// allocating a megabyte per file
var hashBuffers = sync.Pool{
	New: func() any {
		buf := make([]byte, hashBufferSize)
		return &buf
	},
}

// readerOnly hides every method but Read. Without it io.CopyBuffer **silently
// ignores the buffer**: *os.File implements io.WriterTo, CopyBuffer prefers
// that, and its generic fallback allocates its own 32 KiB — so the whole
// change would be a no-op that still looks correct. Nothing is lost by hiding
// it, since File.WriteTo only has a fast path when the destination is a
// socket, and this destination is a hasher.
type readerOnly struct{ io.Reader }

// hashFile computes the BLAKE3 hash of a file, streaming it through a pooled
// buffer so memory stays flat whatever the file size
func hashFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hasher := blake3.New(hashOutputSize, nil)

	buf := hashBuffers.Get().(*[]byte)
	defer hashBuffers.Put(buf)
	if _, err := io.CopyBuffer(hasher, readerOnly{file}, *buf); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}

	sum := make([]byte, 0, hashOutputSize)
	return hex.EncodeToString(hasher.Sum(sum)), nil
}

// store writes the hash and the EXIF columns as one row and marks the file read
func (e *Extractor) store(fileID int64, hash, hashKind string, meta classifier.CommonMetadata) db.DBOperation {
	isScreenshot := 0
	if meta.IsScreenshot {
		isScreenshot = 1
	}

	return func(ctx context.Context, tx *sqlx.Tx) error {
		// A re-read file still has its old metadata row; a plain INSERT would
		// violate UNIQUE(file_hash, file_id) or leave a stale duplicate. No-op
		// for fresh files.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM file_metadata WHERE file_id = ?`, fileID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO file_metadata (
				file_hash, hash_kind, file_id,
				exif_image_width, exif_image_height, exif_orientation,
				exif_gps_latitude, exif_gps_longitude,
				exif_make, exif_model,
				exif_date_time_original, exif_create_date, exif_creation_date, exif_media_create_date,
				is_screenshot
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			hash,
			hashKind,
			fileID,
			db.IntOrNil(meta.ImageWidth),
			db.IntOrNil(meta.ImageHeight),
			db.IntOrNil(meta.Orientation),
			db.FloatOrNil(meta.GPSLatitude),
			db.FloatOrNil(meta.GPSLongitude),
			db.StrOrNil(meta.Make),
			db.StrOrNil(meta.Model),
			db.StrOrNil(meta.DateTimeOriginal),
			db.StrOrNil(meta.CreateDate),
			db.StrOrNil(meta.CreationDate),
			db.StrOrNil(meta.MediaCreateDate),
			isScreenshot,
		); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx,
			`UPDATE file_registry SET scan_status = ? WHERE id = ?`, db.StatusAnalyzed, fileID)
		return err
	}
}

// storeFailure drops the file's stale metadata and parks it at ERROR: its
// content is unknown now, so an old hash and old EXIF would keep feeding the
// scorer and VFS data that no longer describes the bytes on disk
func storeFailure(fileID int64) db.DBOperation {
	return func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM file_metadata WHERE file_id = ?`, fileID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE file_registry SET scan_status = ? WHERE id = ?`, db.StatusError, fileID)
		return err
	}
}
