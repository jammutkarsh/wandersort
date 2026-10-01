package scanner

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// Scanner is stateless across runs — mutable state lives in per-call
// locals, so concurrent scans need no locking on Scanner itself.
type Scanner struct {
	db         *db.DB
	classifier *classifier.FileClassifier
	log        logger.Logger
	path       *path.Resolver
	volumes    *volume.Resolver
	workers    int
}

func New(db *db.DB, log logger.Logger, workers int) *Scanner {
	return &Scanner{
		db:         db,
		classifier: classifier.NewFileClassifier(),
		log:        log,
		path:       path.New(),
		volumes:    volume.New(),
		workers:    workers,
	}
}

// Run scans all paths concurrently and returns how many files it found. force
// replaces every touched row, so later phases re-read the files.
func (s *Scanner) Run(ctx context.Context, paths []string, force bool) (int, error) {
	s.log.Info("Scanner Phase: Processing all paths", "pathCount", len(paths))

	// every row seen is stamped with scan (one past the highest stored); the
	// sweep deletes older stamps. A counter, never a clock: clocks step back.
	var scan int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(last_seen_scan), 0) + 1 FROM file_registry`).Scan(&scan); err != nil {
		return 0, fmt.Errorf("number this scan: %w", err)
	}

	type scanResult struct {
		root   string // canonical absolute root, "" when canonicalization failed
		volume string // the root's volume UUID, "" when unresolved
		count  int
		gaps   walkGaps
		err    error
	}

	// Buffer exactly one result per path; no path produces more than one result
	results := make(chan scanResult, len(paths))

	// Enqueue all work up front so workers can start immediately without blocking
	jobs := make(chan string, len(paths))
	for _, path := range paths {
		jobs <- path
	}

	// Accept no more jobs; workers will stop when they drain the queue
	close(jobs)

	// Spawn workers to drain the job queue concurrently
	var workers sync.WaitGroup
	for range s.workers {
		workers.Go(func() {
			for path := range jobs {
				// Canonicalize once; every stored file_dir and the sweep must
				// agree on the same absolute root spelling
				absRoot, err := s.path.RealPath(path)
				if err != nil {
					s.log.Error("Failed to resolve path", "path", path, "error", err)
					results <- scanResult{err: fmt.Errorf("resolve %s: %w", path, err)}
					continue
				}

				volumeUUID := s.volumes.ForPath(absRoot)
				count, gaps, err := s.scan(ctx, absRoot, volumeUUID, scan, force)
				if err != nil {
					s.log.Error("Failed to scan path", "path", absRoot, "error", err)
					results <- scanResult{root: absRoot, count: count, err: fmt.Errorf("scan failed for %s: %w", path, err)}
					continue
				}

				s.log.Info("Scanned path", "path", absRoot, "filesDiscovered", count)
				results <- scanResult{root: absRoot, volume: volumeUUID, count: count, gaps: gaps}
			}
		})
	}

	// Close results only after all workers are done writing to it
	workers.Wait()
	close(results)

	// flush the queued upserts so the sweep sees every last_seen_scan; a lost
	// upsert leaves its file's stamp old, so sweeping would delete a live row
	flushErr := s.db.Writer.Flush()

	totalFiles := 0
	var firstScanErr error
	for result := range results {
		totalFiles += result.count
		if result.err != nil {
			if firstScanErr == nil {
				firstScanErr = result.err
			}
			continue
		}
		if flushErr != nil {
			continue
		}
		if err := s.sweep(ctx, scan, sweptRoot{root: result.root, volume: result.volume, seen: result.count}, result.gaps); err != nil {
			s.log.Error("Failed to sweep path", "path", result.root, "error", err)
			if firstScanErr == nil {
				firstScanErr = err
			}
		}
	}

	if flushErr != nil {
		return totalFiles, fmt.Errorf("record scanned files (nothing removed): %w", flushErr)
	}
	return totalFiles, firstScanErr
}

// scan walks absRoot and queues one upsert per file it finds, returning how
// many it queued and the parts of the tree the walk could not see.
func (s *Scanner) scan(ctx context.Context, absRoot, volumeUUID string, scan int64, force bool) (int, walkGaps, error) {
	s.log.Info("Scanning path", "path", absRoot)
	// A separate walker goroutine decouples traversal from queueing, so a
	// full writer queue never stalls the directory walk mid-read.
	discoveries := make(chan FileDiscovery, 2*s.workers)
	var gaps walkGaps
	var walkErr error
	go func() {
		defer close(discoveries)
		gaps, walkErr = s.walkRoot(ctx, absRoot, volumeUUID, discoveries)
		if walkErr != nil {
			s.log.Error("Walk root failed", "path", absRoot, "error", walkErr)
		}
	}()

	count := 0
	for file := range discoveries {
		if !s.db.Writer.Write(s.storeScan(file, scan, force)) {
			s.log.Warn("Bulk writer closed; dropping discovery write", "path", file.Name)
			continue
		}
		count++
	}
	// the channel closing is what makes gaps and walkErr safe to read
	return count, gaps, walkErr
}

// walkGaps is what a walk couldn't see: directories it couldn't list, files it
// couldn't stat. Their rows aren't gone, so the sweep keeps them.
type walkGaps struct {
	dirs  []string    // source-path form
	files [][2]string // (file_dir, file_name), source-path form
}

func (g walkGaps) empty() bool { return len(g.dirs) == 0 && len(g.files) == 0 }

// walkRoot walks absRoot (already canonical) and emits FileDiscovery records
// carrying the file's absolute directory and name
func (s *Scanner) walkRoot(ctx context.Context, absRoot, volumeUUID string, output chan<- FileDiscovery) (walkGaps, error) {
	var gaps walkGaps
	err := filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, err error) error {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Handle errors (permission denied, etc.)
		if err != nil {
			// An unreadable root means the whole scan of this path failed —
			// it must not look like "root is empty" (which would sweep the index)
			if p == absRoot {
				return fmt.Errorf("root unreadable: %w", err)
			}
			s.log.Error("Walk error", "inputPath", absRoot, "walkingPath", s.path.RelativeToHome(p), "error", err)
			// WalkDir reports a failure below the root for a directory it
			// could not list: everything under it is unseen, not gone
			gaps.dirs = append(gaps.dirs, path.ToSourcePath(p))
			return nil // Continue walking
		}

		// Skip ignored directories
		if d.IsDir() {
			if s.classifier.ShouldIgnoreDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// skip symlinked files: the recorded size would be the link's. (WalkDir
		// never follows directory links either.)
		if d.Type()&fs.ModeSymlink != 0 {
			s.log.Warn("Skipping a link; add the folder it points to instead", "walkingPath", s.path.RelativeToHome(p))
			return nil
		}

		// Classify file and apply ignore rules in one pass
		mediaType, shouldProcess, shouldIgnore := s.classifier.ClassifyName(d.Name())
		switch {
		case shouldIgnore:
			s.log.Warn("Ignoring file", "inputPath", absRoot, "walkingPath", s.path.RelativeToHome(p))
			return nil
		case !shouldProcess:
			s.log.Warn("Unsupported file type", "walkingPath", s.path.RelativeToHome(p))
			return nil
		}

		// Get file info
		info, err := d.Info()
		if err != nil {
			s.log.Warn("Failed to get file info", "inputPath", absRoot, "walkingPath", s.path.RelativeToHome(p), "error", err)
			gaps.files = append(gaps.files, [2]string{path.ToSourcePath(filepath.Dir(p)), d.Name()})
			return nil
		}

		file := FileDiscovery{
			Dir:        path.ToSourcePath(filepath.Dir(p)),
			Name:       d.Name(),
			Size:       info.Size(),
			ModTime:    info.ModTime(),
			Extension:  strings.ToLower(filepath.Ext(p)),
			VolumeUUID: volumeUUID,
			MediaType:  mediaType,
		}

		// StreamKey: feeds the TUI progress line, stripped from the plain console.
		s.log.Info("Scanning", logger.StreamKey, true, "file", s.path.RelativeToHome(p))

		// Send to processing channel
		select {
		case output <- file:
		case <-ctx.Done():
			return ctx.Err()
		}

		return nil
	})
	if err != nil {
		return gaps, fmt.Errorf("walk %q: %w", absRoot, err)
	}
	return gaps, nil
}

// maxSweepGaps bounds the exclusions a sweep spells out; past it the walk saw
// too little and the sweep is skipped.
const maxSweepGaps = 1000

// sweptRoot is one cleanly walked root as the sweep needs it: its canonical
// path, the volume it was on, and how many files the walk saw under it.
type sweptRoot struct {
	root, volume string
	seen         int
}

// sweep hard-deletes rows under root this scan didn't see (older
// last_seen_scan); metadata, plan and error rows cascade. Only for roots that
// walked cleanly. Placed files have library-relative paths, never under a root.
//
// Rows are kept where the walk may not have been looking:
//   - under gaps (unlistable folders, unstattable files);
//   - on a different volume than the one now at root (another card mounted at
//     the same path);
//   - under a root that walked clean but empty while rows say it held files
//     (an unmounted drive's mount point).
func (s *Scanner) sweep(ctx context.Context, scan int64, r sweptRoot, gaps walkGaps) error {
	root := r.root
	if len(gaps.dirs)+len(gaps.files) > maxSweepGaps {
		s.log.Warn("Skipping cleanup of vanished files: too much of this folder could not be read",
			logger.UserKey, true, "path", root, "unreadable", len(gaps.dirs)+len(gaps.files))
		return nil
	}
	// range match on file_dir: seeks, and needs no LIKE escaping. Convert
	// root the way file_dir is stored, and trim the trailing separator, or
	// "/" becomes the range ["//", "/0") which matches nothing.
	trimmed := strings.TrimSuffix(path.ToSourcePath(root), "/")
	prefix := trimmed + "/"
	prefixEnd := trimmed + string(rune('/'+1))

	if r.seen == 0 {
		var known int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_registry WHERE `+underDir,
			trimmed, prefix, prefixEnd).Scan(&known); err != nil {
			return fmt.Errorf("sweep %q: %w", root, err)
		}
		if known > 0 {
			s.log.Warn("Found no files in a folder the library knows files in; kept them — if the drive was not mounted, nothing was forgotten",
				logger.UserKey, true, "path", root, "known", known)
			return nil
		}
	}

	query := `SELECT id FROM file_registry WHERE last_seen_scan < ? AND ` + underDir +
		` AND (volume_uuid IS NULL OR ? = '' OR volume_uuid = ?)`
	args := []any{scan, trimmed, prefix, prefixEnd, r.volume, r.volume}
	if !gaps.empty() {
		for _, d := range gaps.dirs {
			d = strings.TrimSuffix(d, "/")
			query += ` AND NOT ` + underDir
			args = append(args, d, d+"/", d+string(rune('/'+1)))
		}
		for _, f := range gaps.files {
			query += ` AND NOT (file_dir = ? AND file_name = ?)`
			args = append(args, f[0], f[1])
		}
	}

	// the registry rows alone: metadata, plan and error rows cascade
	var swept int
	if err := s.db.Writer.WriteSync(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
		var ids []int64
		if err := tx.SelectContext(ctx, &ids, query, args...); err != nil {
			return err
		}
		swept = len(ids)
		return db.Forget(ctx, tx, ids)
	}); err != nil {
		return fmt.Errorf("sweep %q: %w", root, err)
	}

	if swept > 0 {
		s.log.Info("Removed vanished files", "path", root, "filesRemoved", swept)
	}
	return nil
}

// underDir matches a row in a directory or below it, as a range (dir, dir+"/",
// dir+"0").
const underDir = `(file_dir = ? OR (file_dir >= ? AND file_dir < ?))`

// storeScan builds the BulkWriter op for one discovered file. It must touch
// nothing outside tx: a failed batch replays every op.
func (s *Scanner) storeScan(file FileDiscovery, scan int64, force bool) db.DBOperation {
	const query = `
		INSERT INTO file_registry (
			file_dir, file_name, file_size, file_modified_at,
			volume_uuid, media_type, file_extension,
			file_origin,
			discovered_at, last_seen_at, last_seen_scan
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (file_dir, file_name) DO UPDATE SET
			last_seen_at = excluded.last_seen_at,
			last_seen_scan = excluded.last_seen_scan,
			file_origin = excluded.file_origin,
			volume_uuid = COALESCE(excluded.volume_uuid, file_registry.volume_uuid)`

	return func(ctx context.Context, tx *sqlx.Tx) error {
		now := db.FormatTime(time.Now())
		modifiedAt := db.FormatTime(file.ModTime)
		// a changed file (or any under force) is a new file: forget the old row
		// and let the insert make a fresh one. An error rolls both back; the
		// writer replays the batch without this op and reports it at Flush.
		if err := db.ForgetChanged(ctx, tx, file.Dir, file.Name, file.Size, modifiedAt, force); err != nil {
			return fmt.Errorf("replace changed %s: %w", file.Name, err)
		}
		if _, err := tx.ExecContext(ctx, query,
			file.Dir, file.Name, file.Size, modifiedAt,
			db.StrOrNil(file.VolumeUUID), file.MediaType, file.Extension,
			FileOriginSource, now, now, scan,
		); err != nil {
			return fmt.Errorf("upsert %s: %w", file.Name, err)
		}
		return nil
	}
}
