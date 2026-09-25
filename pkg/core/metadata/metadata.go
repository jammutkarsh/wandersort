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
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"
	"golang.org/x/sync/semaphore"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	wspath "github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/volume"
	"lukechampine.com/blake3"
)

// hashOutputSize is the output length of BLAKE3-256 in bytes
const hashOutputSize = 32

// hashPrefix names the algorithm in every stored file_hash, so a later change
// of algorithm can never be mistaken for a match
const hashPrefix = "blake3:"

// maxReadBudget caps concurrent byte reads regardless of how many cores the
// machine has. NVMe queues are deep, but random-read IOPS plateau somewhere
// around 16-32 outstanding requests — past that it is the same throughput at
// worse latency. The cap is on reads only: exiftool is a CPU-bound Perl
// process, and a 64-core box genuinely wants 64 of those.
const maxReadBudget = 16

// unreadFiles is the one definition of "still to read": no metadata row. The
// count, the volume grouping and the producer's pages all ask through it, so
// they cannot disagree about what is left. Nothing is written to hand a file
// out — the predicate only shrinks as workers store their rows.
//
// A file that failed to read before is still unread and is tried again every
// run: most read failures are a card reader or a USB cable, gone by the next
// add, and a file skipped for good is a photo left out of the library that
// nobody is told about. Within one run the forward-only cursor hands each
// file out once, so a failure costs one attempt per run.
//
// ponytail: a file exiftool hangs on costs its full timeout on every add;
// skip after N attempts (errors.attempts) if that ever adds up.
const unreadFiles = `
	FROM file_registry f
	WHERE NOT EXISTS (SELECT 1 FROM file_metadata m WHERE m.file_id = f.id)`

// readBatchSize is how many unread files one page asks for. 256 keeps the
// worker channel (2*workers) fed without holding a long-running statement open.
const readBatchSize = 256

// fileRecord is what a page of unread files hands a worker: mediaType is carried so a
// sidecar can be hashed without paying for an exiftool call it has no tags
// for, and cost is what reading it charges the shared read budget
type fileRecord struct {
	id        int64
	absPath   string
	mediaType string
	cost      int64
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

// Run pages through every unread file and reads it in a bounded worker pool.
// Returns how many files were persisted
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

	// Taken before any file is handed out, so the closing count below is this
	// run's failures: every failed file is tried again and its row's
	// last_seen_at moves on, and one that read fine this time has no row.
	// Timestamps are fixed-width, so they compare as text.
	runStartedAt := db.FormatTime(time.Now())

	var total int
	if err := e.db.QueryRowContext(ctx, `SELECT COUNT(*) `+unreadFiles).Scan(&total); err != nil {
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
	// Workers stop early only on shutdown or a closed writer. The first
	// already cancels; the second doesn't, and a producer blocked handing out
	// the next file would then wait forever for a worker that is gone.
	cancel()

	if err := <-producerErr; err != nil {
		if ctx.Err() == nil && errors.Is(err, context.Canceled) {
			return 0, errors.New("metadata: the database writer closed before every file was read")
		}
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	persisted := int(extracted.Load())
	e.log.Info("Metadata extraction complete", "filesRead", persisted)
	// Files that failed are tried again next run; say how many failed this one.
	e.db.Writer.Flush()
	var unreadable int
	if err := e.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM errors WHERE stage = ? AND last_seen_at >= ?`,
		db.StageRead, runStartedAt).Scan(&unreadable); err == nil && unreadable > 0 {
		files := "files"
		if unreadable == 1 {
			files = "file"
		}
		e.log.Warn(fmt.Sprintf("%d %s could not be read — see the log", unreadable, files),
			logger.UserKey, true)
	}
	return persisted, nil
}

// producer drains one volume at a time, fastest first, and feeds the workers.
// Ordering by device does not change total wall time — the same bytes are read
// either way — but it front-loads progress, so an interrupted run has the cheap
// files done and the progress bar moves early instead of crawling behind an
// HDD. Within a volume the order is untouched: id is discovery order is walk
// order is roughly directory order, which is as seek-friendly as a read order
// gets.
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

	// The final pass is unscoped: it catches any volume the grouping missed,
	// so no row can be stranded by an edge case in the query above. Volumes
	// already drained are excluded rather than re-read — their last files may
	// still be in flight, so the unread predicate can still see them.
	drained := make([]string, 0, len(volumes))
	for _, v := range append(volumes, pendingVolume{all: true}) {
		excluded, err := json.Marshal(drained)
		if err != nil {
			fail(fmt.Errorf("list drained volumes: %w", err))
			return
		}
		// Forward-only cursor: the predicate only ever shrinks, so it can
		// neither repeat a file nor skip one.
		var cursor int64
		for {
			batch, err := e.nextBatch(ctx, v, cursor, string(excluded))
			if err != nil {
				fail(err)
				return
			}
			if len(batch) == 0 {
				break // this volume is drained; move to the next
			}
			for _, record := range batch {
				select {
				case toRead <- record:
				case <-ctx.Done():
					producerErr <- ctx.Err()
					return
				}
			}
			cursor = batch[len(batch)-1].id
		}
		drained = append(drained, v.uuid)
	}
	producerErr <- nil
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
		SELECT COALESCE(f.volume_uuid, ''), MIN(f.file_dir) `+unreadFiles+`
		GROUP BY COALESCE(f.volume_uuid, '')`)
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
		sampleDir = wspath.FromSourcePath(sampleDir)
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

// nextBatch pages the unread files of one volume — of every volume not in
// excluded (a JSON array of uuids), during the closing sweep — starting after
// cursor. Reading claims nothing: a file is handed out by the producer, and
// leaves the unread set only when its worker stores a row. An interrupted run
// wrote nothing for the files in flight, so they are simply read again.
func (e *Extractor) nextBatch(ctx context.Context, v pendingVolume, cursor int64, excluded string) ([]fileRecord, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT f.id, f.file_dir, f.file_name, COALESCE(f.media_type, ''), COALESCE(f.volume_uuid, '') `+unreadFiles+`
		  AND (? OR COALESCE(f.volume_uuid, '') = ?)
		  AND COALESCE(f.volume_uuid, '') NOT IN (SELECT value FROM json_each(?))
		  AND f.id > ?
		ORDER BY f.id
		LIMIT ?`, v.all, v.uuid, excluded, cursor, readBatchSize)
	if err != nil {
		return nil, fmt.Errorf("page unread files: %w", err)
	}
	defer rows.Close()

	var batch []fileRecord
	for rows.Next() {
		var id int64
		var fileDir, fileName, mediaType, uuid string
		if err := rows.Scan(&id, &fileDir, &fileName, &mediaType, &uuid); err != nil {
			return nil, fmt.Errorf("scan unread file: %w", err)
		}
		fileDir = wspath.FromSourcePath(fileDir)
		cost := v.cost
		if v.all {
			// the closing sweep has no price of its own: charge the straggler by
			// whatever volume it turned out to be on
			cost = readCost(e.classOf(uuid, fileDir), e.budget)
		}
		batch = append(batch, fileRecord{
			id:        id,
			absPath:   filepath.Join(fileDir, fileName),
			mediaType: mediaType,
			cost:      cost,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("page unread files: %w", err)
	}
	return batch, nil
}

// worker reads files until the channel closes. A panic on one file is
// recorded and survived, so one bad file cannot end a six-hour scan.
func (e *Extractor) worker(ctx context.Context, toRead <-chan fileRecord, extracted *atomic.Int64, total int) {
	for file := range toRead {
		if ctx.Err() != nil {
			return
		}
		if !e.readOne(ctx, file, extracted, total) {
			return
		}
	}
}

// Steps of a read, as recorded in errors.op.
const (
	opOpen     = "open"
	opHash     = "hash"
	opExiftool = "exiftool"
	opStore    = "store"
)

// readOne hashes one file, reads its EXIF while the bytes are still cached, and
// enqueues the single write that persists both. It reports false when the
// worker should stop (shutdown, or the writer closed).
func (e *Extractor) readOne(ctx context.Context, file fileRecord, extracted *atomic.Int64, total int) (keepGoing bool) {
	op := opHash
	defer func() {
		if r := recover(); r != nil {
			err := db.PanicError(r)
			e.log.Error("Panic while reading file", "fileId", file.id, "path", file.absPath, "op", op, "error", err)
			e.db.Writer.Write(storeFailure(file.id, op, err))
			keepGoing = true
		}
	}()

	sum, err := e.readFile(ctx, file)
	if err != nil {
		// A cancelled pipeline aborts the budget wait rather than the
		// read; that is shutdown, not a bad file, so don't record it.
		if ctx.Err() != nil {
			return false
		}
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) && pathErr.Op == opOpen {
			op = opOpen
		}
		e.log.Error("Failed to hash file", "fileId", file.id, "path", file.absPath, "error", err)
		e.db.Writer.Write(storeFailure(file.id, op, db.WithStack(err)))
		return true
	}

	// A failed extraction is not a failed file: the pipeline still knows the
	// file's hash and its folder context, so the VFS can place it. Persist
	// the empty metadata and move on — no errors row
	var meta classifier.CommonMetadata
	// Sidecars (iPhone .AAE edit files) carry no EXIF of their own, so
	// spawning exiftool on them is pure waste — hash them and move on
	if file.mediaType != classifier.MediaTypeSidecar {
		op = opExiftool
		var err error
		if e.pool != nil {
			meta, err = e.pool.Extract(ctx, file.absPath)
		} else {
			err = fmt.Errorf("exiftool not available")
		}
		if err != nil {
			// A cancelled pipeline kills the exiftool child mid-call — that
			// is shutdown, not a bad file, so don't report it as an
			// extraction failure.
			if ctx.Err() != nil {
				return false
			}
			// exiftool itself died or hung: the file's tags are unknown, not
			// empty. Persisting an empty row would mark the file read and plan
			// it by file date alone, for good — so record a READ failure
			// instead, which the end-of-run count reports and the next run retries.
			if errors.Is(err, exiftool.ErrProcess) {
				e.log.Error("exiftool failed on file", "fileId", file.id, "path", file.absPath, "error", err)
				e.db.Writer.Write(storeFailure(file.id, opExiftool, db.WithStack(err)))
				return true
			}
			e.log.Warn("Failed to extract exif data", "fileId", file.id, "path", file.absPath, "error", err)
		}
	}
	op = opStore

	// StreamKey: feeds the TUI progress bar, stripped from the plain console.
	e.log.Info("Reading", logger.StreamKey, true,
		"file", filepath.Base(file.absPath), "extracted", extracted.Add(1), "total", total)

	if !e.db.Writer.Write(e.store(file.id, sum, meta)) {
		e.log.Warn("Bulk writer closed; dropping metadata write", "fileId", file.id)
		return false
	}
	return true
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
	return HashFile(file.absPath)
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

// NewHasher returns the hasher every stored file_hash is computed with.
// Execute feeds the bytes it copies through one, so the copy is checked
// against the scan without reading anything twice.
func NewHasher() hash.Hash { return blake3.New(hashOutputSize, nil) }

// HashString is h's sum spelled the way file_hash stores it — the algorithm,
// then hex — so a caller compares against the database without ever writing
// the prefix itself.
func HashString(h hash.Hash) string {
	return hashPrefix + hex.EncodeToString(h.Sum(make([]byte, 0, hashOutputSize)))
}

// HashFile computes a file's file_hash, streaming it through a pooled buffer
// so memory stays flat whatever the file size
func HashFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hasher := NewHasher()

	buf := hashBuffers.Get().(*[]byte)
	defer hashBuffers.Put(buf)
	if _, err := io.CopyBuffer(hasher, readerOnly{file}, *buf); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}
	return HashString(hasher), nil
}

// store writes the hash and the EXIF columns as one row — which is what marks
// the file read — and clears the READ failure the row's existence supersedes
func (e *Extractor) store(fileID int64, sum string, meta classifier.CommonMetadata) db.DBOperation {
	isScreenshot := 0
	if meta.IsScreenshot {
		isScreenshot = 1
	}

	return func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM errors WHERE file_id = ? AND stage = ?`, fileID, db.StageRead); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO file_metadata (
				file_hash, file_id,
				exif_image_width, exif_image_height, exif_orientation,
				exif_gps_latitude, exif_gps_longitude,
				exif_make, exif_model,
				exif_date_time_original, exif_create_date, exif_creation_date, exif_media_create_date,
				is_screenshot
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sum,
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
		)
		return err
	}
}

// storeFailure records why the file could not be read, bumping attempts on a
// file that failed before. The file stays unread, so the next run tries again.
func storeFailure(fileID int64, op string, err error) db.DBOperation {
	return func(ctx context.Context, tx *sqlx.Tx) error {
		return db.RecordError(ctx, tx, fileID, db.StageRead, op, err)
	}
}
