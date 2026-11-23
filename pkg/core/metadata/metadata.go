// Package metadata is the pipeline's only pass that reads file bytes: each
// worker hashes a file, then runs exiftool on it while the page cache is warm.
// Keep the two together; split, every file is read from disk twice.
package metadata

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
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

	"github.com/jammutkarsh/wandersort/pkg/atomicfile"
	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	wspath "github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// Every stored hash names its algorithm, so a later change of algorithm can
// never be mistaken for a match.
const (
	md5Prefix    = "md5:"
	sha256Prefix = "sha256:"
)

// sums are a file's two hashes. MD5 is what groups files; SHA-256 confirms the
// match, and is what a copy and a full check verify against.
type sums struct{ md5, sha256 string }

// maxReadBudget caps concurrent byte reads: random-read IOPS plateau around
// 16-32 outstanding requests. Only reads are capped; exiftool is CPU-bound.
const maxReadBudget = 16

// unreadFiles is the one definition of "still to read": no metadata row.
// Handing a file out writes nothing. A file that failed before is retried
// every run (most failures are a cable or card reader); the forward-only cursor
// tries it once per run.
//
// a file exiftool hangs on costs its full timeout every add; skip
// after N attempts (errors.attempts) if that adds up.
const unreadFiles = `
	FROM file_registry f
	WHERE NOT EXISTS (SELECT 1 FROM file_metadata m WHERE m.file_id = f.id)`

// readBatchSize is how many unread files one page asks for. 256 keeps the
// worker channel (2*workers) fed without holding a long-running statement open.
const readBatchSize = 256

// fileRecord is one unread file for a worker: mediaType lets a sidecar skip
// exiftool, cost is its charge against the read budget.
type fileRecord struct {
	absPath   string
	mediaType string
	id        int64
	cost      int64
	size      int64 // as the scan recorded it
}

// Extractor hashes discovered files and reads their EXIF in one pass
type Extractor struct {
	// Progress, when set, hears each file handled (read, or failed and
	// recorded), with the count so far and the total.
	Progress func(handled, total int)

	ran  atomic.Bool // Run closes the pool, so it runs once
	log  logger.Logger
	db   *db.DB
	pool *exiftool.Pool
	path *wspath.Resolver

	// reads admits byte reads by storage class: a spinning disk charges the
	// whole budget, an SSD charges 1.
	//
	// one shared budget couples independent devices (an HDD read
	// throttles an idle SSD), only at volume boundaries since volumes drain
	// one at a time. Per-volume budgets if a mixed library measures badly.
	reads *semaphore.Weighted
	// classes caches the storage class per volume UUID. Producer-only, so it
	// needs no lock.
	classes map[string]volume.Class
	workers int

	budget int64
}

// New starts the exiftool pool. Without it every file would be stored with
// empty metadata and never read again, so a pool that won't start is an error.
func New(database *db.DB, log logger.Logger, exiftoolPath string, workers int) (*Extractor, error) {
	pool, err := exiftool.NewPool(exiftoolPath, workers)
	if err != nil {
		return nil, fmt.Errorf("start exiftool: %w", err)
	}

	budget := int64(min(max(workers, 1), maxReadBudget))
	return &Extractor{
		db:      database,
		log:     log,
		pool:    pool,
		path:    wspath.New(),
		workers: workers,
		reads:   semaphore.NewWeighted(budget),
		budget:  budget,
		classes: map[string]volume.Class{},
	}, nil
}

// readTargets is how many files of a class may be read at once with the full
// budget. Concurrent readers on a spinning disk cost more in seeks than they
// overlap.
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

// ErrRanOnce refuses a second Run: the first one closes the exiftool pool.
var ErrRanOnce = errors.New("metadata: an extractor runs once; build another with New")

// Run pages through every unread file and reads it in a bounded worker pool,
// once per Extractor (ErrRanOnce after). Returns how many files were persisted.
func (e *Extractor) Run(ctx context.Context) (int, error) {
	if !e.ran.CompareAndSwap(false, true) {
		return 0, ErrRanOnce
	}
	defer e.pool.Close()

	toRead := make(chan fileRecord, 2*e.workers)
	producerErr := make(chan error, 1)

	var extracted, handled atomic.Int64

	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	e.log.Info("Extracting metadata")

	// taken before any file is handed out, so the closing count is this
	// run's failures (fixed-width timestamps compare as text)
	runStartedAt := db.FormatTime(time.Now())

	var total int
	if err := e.db.QueryRowContext(ctx, `SELECT COUNT(*) `+unreadFiles).Scan(&total); err != nil {
		e.log.Warn("Failed to count files to read", "error", err)
	}

	go e.producer(ctxWithCancel, cancel, toRead, producerErr)

	// workers write through the BulkWriter directly; it already serializes
	var wg sync.WaitGroup
	for range e.workers {
		wg.Go(func() {
			e.worker(ctxWithCancel, toRead, &extracted, &handled, total)
		})
	}
	wg.Wait()
	// a closed writer doesn't cancel ctx, so cancel here or the producer
	// blocks forever
	cancel()

	if err := <-producerErr; err != nil {
		if ctx.Err() == nil && errors.Is(err, context.Canceled) {
			return 0, fmt.Errorf("metadata: the database %w before every file was read", db.ErrWriterClosed)
		}
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	persisted := int(extracted.Load())
	e.log.Info("Metadata extraction complete", "filesRead", persisted)
	if err := e.db.Writer.Flush(); err != nil {
		return 0, fmt.Errorf("record what was read: %w", err)
	}
	// Files that failed are tried again next run; say how many failed this one.
	var unreadable int
	if err := e.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM errors WHERE stage = ? AND last_seen_at >= ?`,
		db.StageRead, runStartedAt).Scan(&unreadable); err == nil && unreadable > 0 {
		files := "files"
		if unreadable == 1 {
			files = "file"
		}
		e.log.Warn(fmt.Sprintf("%d %s could not be read; they're tried again next time", unreadable, files),
			logger.UserKey, true)
	}
	return persisted, nil
}

// producer drains one volume at a time, fastest first, so an interrupted run
// has the cheap files done. Within a volume, id order is walk order: keep it,
// it is the most seek-friendly order available.
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

	// the final pass is unscoped, catching any volume the grouping missed;
	// drained volumes are excluded (their last files may still be in flight)
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

// pendingVolume is one volume's unread work. all marks the closing sweep,
// which differs from an empty uuid (files whose volume never resolved).
type pendingVolume struct {
	uuid string
	cost int64
	all  bool
}

// pendingVolumes groups the unread files by volume and prices each.
func (e *Extractor) pendingVolumes(ctx context.Context) ([]pendingVolume, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT f.volume_uuid, MIN(f.file_dir) `+unreadFiles+`
		GROUP BY f.volume_uuid`)
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
		e.log.Info("Storage detected",
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

// classOf resolves and caches a volume's storage class. An unresolved UUID
// gets ClassUnknown without a lookup: the class would fail the same way.
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

// nextBatch pages one volume's unread files after cursor (during the closing
// sweep, every volume not in excluded, a JSON uuid array). Claims nothing:
// files in flight when a run stops are simply read again.
func (e *Extractor) nextBatch(ctx context.Context, v pendingVolume, cursor int64, excluded string) ([]fileRecord, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT f.id, f.file_dir, f.file_name, COALESCE(f.media_type, ''), f.volume_uuid, f.file_size `+unreadFiles+`
		  AND (? OR f.volume_uuid = ?)
		  AND f.volume_uuid NOT IN (SELECT value FROM json_each(?))
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
		var size int64
		if err := rows.Scan(&id, &fileDir, &fileName, &mediaType, &uuid, &size); err != nil {
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
			size:      size,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("page unread files: %w", err)
	}
	return batch, nil
}

// worker reads files until the channel closes. A panic on one file is
// recorded and survived, so one bad file cannot end a six-hour scan.
func (e *Extractor) worker(ctx context.Context, toRead <-chan fileRecord, extracted, handled *atomic.Int64, total int) {
	for file := range toRead {
		if ctx.Err() != nil {
			return
		}
		if !e.readOne(ctx, file, extracted, total) {
			return
		}
		// a failed file counts too, or a run with one ends short of its total
		n := int(handled.Add(1))
		if e.Progress != nil {
			e.Progress(n, total)
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

// readOne hashes one file, reads its EXIF while cached, and enqueues one write
// for both. Returns false when the worker should stop.
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

	read, err := e.readFile(ctx, file)
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

	// an unparseable tag isn't a failed file: hash and folder still place it,
	// so persist empty metadata with no errors row
	var meta classifier.CommonMetadata
	// sidecars (.AAE edit files) carry no EXIF: hash only
	if file.mediaType != classifier.MediaTypeSidecar {
		op = opExiftool
		var err error
		meta, err = e.pool.Extract(ctx, file.absPath)
		if err != nil {
			// cancelled pipeline killed exiftool: shutdown, not a bad file
			if ctx.Err() != nil {
				return false
			}
			// exiftool died, hung, could not open the file or cannot be given
			// its path: tags unknown, not empty. An empty row would plan the
			// file by its file date for good, so record a READ error and retry
			// next run.
			if errors.Is(err, exiftool.ErrProcess) || errors.Is(err, exiftool.ErrUnreadable) || errors.Is(err, exiftool.ErrUnsafePath) {
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
		"file", e.path.RelativeToHome(file.absPath), "extracted", extracted.Add(1), "total", total)

	if !e.db.Writer.Write(e.store(file.id, read, meta)) {
		e.log.Warn("Bulk writer closed; dropping metadata write", "fileId", file.id)
		return false
	}
	return true
}

// ErrChangedSinceScan means a file's bytes are not the size the scan
// recorded: it was still being written. Its hash would not describe the row,
// so the file waits for the next scan to record it afresh.
var ErrChangedSinceScan = errors.New("file changed since it was scanned")

// readFile gates the byte read on the storage budget. exiftool stays outside
// the gate: it reads a warm header and is CPU-bound.
func (e *Extractor) readFile(ctx context.Context, file fileRecord) (sums, error) {
	if err := e.reads.Acquire(ctx, file.cost); err != nil {
		return sums{}, err // cancelled
	}
	defer e.reads.Release(file.cost)
	m, s := md5.New(), NewHasher()
	n, err := hashInto(ctx, file.absPath, io.MultiWriter(m, s))
	if err != nil {
		return sums{}, err
	}
	if n != file.size {
		return sums{}, fmt.Errorf("%w: read %d bytes, scanned %d", ErrChangedSinceScan, n, file.size)
	}
	return sums{md5: md5Prefix + hex.EncodeToString(m.Sum(nil)), sha256: HashString(s)}, nil
}

// hashBufferSize is bytes per read syscall: 32× fewer syscalls than io.Copy's
// 32 KiB default, and a bigger read-ahead window.
const hashBufferSize = 1 << 20

// hashBuffers keeps one buffer per in-flight read alive rather than
// allocating a megabyte per file
var hashBuffers = sync.Pool{
	New: func() any {
		buf := make([]byte, hashBufferSize)
		return &buf
	},
}

// NewHasher returns the hasher every stored file_sha256 is computed with.
func NewHasher() hash.Hash { return sha256.New() }

// HashString is h's sum as file_sha256 stores it: algorithm prefix, then hex.
func HashString(h hash.Hash) string {
	return sha256Prefix + hex.EncodeToString(h.Sum(nil))
}

// HashFile computes a file's file_sha256, streaming it through a pooled buffer
// so memory stays flat whatever the file size; it stops between reads once
// ctx ends.
func HashFile(ctx context.Context, filePath string) (string, error) {
	h := NewHasher()
	if _, err := hashInto(ctx, filePath, h); err != nil {
		return "", err
	}
	return HashString(h), nil
}

// hashInto streams filePath into w and returns the bytes it read.
func hashInto(ctx context.Context, filePath string, w io.Writer) (int64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()
	// ContextReader also hides *os.File's WriteTo: io.CopyBuffer prefers it,
	// and its fallback ignores our buffer, silently
	r := atomicfile.ContextReader{Ctx: ctx, R: file}

	buf := hashBuffers.Get().(*[]byte)
	defer hashBuffers.Put(buf)
	n, err := io.CopyBuffer(w, r, *buf)
	if err != nil {
		return 0, fmt.Errorf("failed to hash file: %w", err)
	}
	return n, nil
}

// store writes the hashes and the EXIF columns as one row — which is what marks
// the file read — and clears the READ failure the row's existence supersedes.
// An upsert: a batch whose commit failed may have landed, and the writer replays it.
func (e *Extractor) store(fileID int64, read sums, meta classifier.CommonMetadata) db.DBOperation {
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
				file_hash, file_sha256, file_id,
				exif_image_width, exif_image_height, exif_orientation,
				exif_gps_latitude, exif_gps_longitude,
				exif_make, exif_model,
				exif_date_time_original, exif_create_date, exif_creation_date, exif_media_create_date,
				is_screenshot
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (file_id) DO UPDATE SET
				file_hash = excluded.file_hash,
				file_sha256 = excluded.file_sha256,
				exif_image_width = excluded.exif_image_width,
				exif_image_height = excluded.exif_image_height,
				exif_orientation = excluded.exif_orientation,
				exif_gps_latitude = excluded.exif_gps_latitude,
				exif_gps_longitude = excluded.exif_gps_longitude,
				exif_make = excluded.exif_make,
				exif_model = excluded.exif_model,
				exif_date_time_original = excluded.exif_date_time_original,
				exif_create_date = excluded.exif_create_date,
				exif_creation_date = excluded.exif_creation_date,
				exif_media_create_date = excluded.exif_media_create_date,
				is_screenshot = excluded.is_screenshot`,
			read.md5,
			read.sha256,
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
