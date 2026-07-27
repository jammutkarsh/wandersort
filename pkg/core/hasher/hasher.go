// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package hasher

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"lukechampine.com/blake3"
)

const (
	// hashOutputSize is the output length of BLAKE3-256 in bytes
	hashOutputSize = 32
)

// Hasher handles file hashing and content group management. It only computes
// the content hash — the metadata row it inserts is filled in afterwards by
// the exif phase (pkg/core/exif)
type Hasher struct {
	db      *db.DB
	log     logger.Logger
	workers int
}

func New(db *db.DB, log logger.Logger, workers int) *Hasher {
	return &Hasher{
		db:      db,
		log:     log,
		workers: workers,
	}
}

// Run fetches every DISCOVERED file in pages and executes hashing in bounded
// worker pools
func (h *Hasher) Run(ctx context.Context) (int, error) {
	toHash := make(chan fileRecord, 2*h.workers)
	toStore := make(chan hashedRecord, 2*h.workers)
	producerErr := make(chan error, 1)
	hasherErr := make(chan error, 1)

	var hashedCount atomic.Int64

	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	h.log.Info("Hashing")

	var total int
	if err := h.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM live_files WHERE scan_status = ?
	`, db.StatusDiscovered).Scan(&total); err != nil {
		h.log.Warn("Failed to count files to hash", "error", err)
	}

	go h.producer(ctxWithCancel, cancel, toHash, producerErr)
	go h.hasher(ctxWithCancel, cancel, toHash, toStore, total)
	go h.store(ctxWithCancel, cancel, toStore, &hashedCount, hasherErr)

	if err := <-producerErr; err != nil {
		return 0, err
	}
	if err := <-hasherErr; err != nil {
		return 0, err
	}

	persisted := int(hashedCount.Load())
	h.log.Info("Hashing complete", "filesHashed", persisted)
	return persisted, nil
}

// producer fetches hashable files from the database and feeds them into the toHash channel
func (h *Hasher) producer(ctx context.Context, cancel context.CancelFunc, toHash chan<- fileRecord, producerErr chan<- error) {
	defer close(toHash)

	for {
		record, ok, err := h.getFile(ctx)
		if err != nil {
			producerErr <- err
			cancel()
			return
		}
		if !ok {
			producerErr <- nil
			return
		}

		select {
		case toHash <- record:
		case <-ctx.Done():
			producerErr <- ctx.Err()
			return
		}
	}
}

// getFile atomically claims the next undiscovered file and returns its record
func (h *Hasher) getFile(ctx context.Context) (fileRecord, bool, error) {
	var id int64
	var fileDir, fileName string
	query := `
	UPDATE file_registry
	SET scan_status = ?
	WHERE id = (
		SELECT id
		FROM live_files
		WHERE scan_status = ?
		ORDER BY id
		LIMIT 1
	)
	RETURNING id, file_dir, file_name`

	err := h.db.
		QueryRowContext(ctx, query, db.StatusHashing, db.StatusDiscovered).
		Scan(&id, &fileDir, &fileName)
	if errors.Is(err, sql.ErrNoRows) {
		return fileRecord{}, false, nil
	}
	if err != nil {
		return fileRecord{}, false, fmt.Errorf("claim next hash row: %w", err)
	}

	return fileRecord{id: id, absPath: filepath.Join(fileDir, fileName)}, true, nil
}

// hasher runs the bounded worker pool that computes BLAKE3 hashes
func (h *Hasher) hasher(ctx context.Context, cancel context.CancelFunc, toHash <-chan fileRecord, toPersist chan<- hashedRecord, total int) {
	var hashedN atomic.Int64
	var hashWG sync.WaitGroup

	for range h.workers {
		hashWG.Go(func() {
			for file := range toHash {
				if ctx.Err() != nil {
					return
				}

				hash, err := h.hashFile(file.absPath)
				if err != nil {
					h.log.Error("Failed to hash file", "fileId", file.id, "path", file.absPath, "error", err)
					h.db.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
						// The file's content is unknown now, so any previous
						// metadata row (old hash, old EXIF) is stale — drop it
						// rather than let the scorer/VFS keep acting on it
						if _, err := tx.ExecContext(ctx,
							`DELETE FROM file_metadata WHERE file_id = ?`, file.id); err != nil {
							return err
						}
						_, err := tx.ExecContext(ctx, ` UPDATE file_registry
							SET scan_status = 'ERROR' WHERE id = ?`, file.id)
						return err
					})
					continue
				}
				// Per-file feed line for the TUI (StreamKey: feed + file log, never
				// the plain console). Logged at Info — the TUI handler filters by
				// level, so a Debug line would vanish outside --debug and the bar
				// would never move. It carries the running count so the bar
				// advances one file at a time.
				h.log.Info("Hashing", logger.StreamKey, true,
					"file", filepath.Base(file.absPath), "hashed", hashedN.Add(1), "total", total)

				select {
				case toPersist <- hashedRecord{id: file.id, hash: hash}:
				case <-ctx.Done():
					return
				}
			}
		})
	}

	go func() {
		hashWG.Wait()
		close(toPersist)
		if ctx.Err() != nil {
			cancel()
		}
	}()
}

// hashFile computes the BLAKE3 hash of a file
// Uses streaming to handle files of any size with constant memory (~32KB buffer)
func (h *Hasher) hashFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hasher := blake3.New(hashOutputSize, nil)

	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}

	sum := make([]byte, 0, hashOutputSize)
	hash := hex.EncodeToString(hasher.Sum(sum))
	return hash, nil
}

// store consumes hashed records and writes them to the database via BulkWriter
func (h *Hasher) store(ctx context.Context, cancel context.CancelFunc, files <-chan hashedRecord, hashed *atomic.Int64, persistErr chan<- error) {
	for file := range files {
		select {
		case <-ctx.Done():
			persistErr <- ctx.Err()
			cancel()
			return
		default:
		}

		ok := h.db.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			// A re-hashed file (size/mtime change reset it to DISCOVERED) still
			// has its old metadata row; a plain INSERT would either violate
			// UNIQUE(file_hash, file_id) or leave a stale duplicate in the
			// scorer's hash grouping. Fresh files make this a no-op
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM file_metadata WHERE file_id = ?`, file.id); err != nil {
				return err
			}
			// The exif_* columns stay NULL here — the exif phase fills them in
			// on this row once every file has a hash
			_, err := tx.ExecContext(ctx,
				`INSERT INTO file_metadata (file_hash, file_id) VALUES (?, ?)`,
				file.hash, file.id)
			return err
		})
		if !ok {
			persistErr <- fmt.Errorf("bulk writer closed")
			cancel()
			return
		}

		if err := ctx.Err(); err != nil {
			persistErr <- err
			cancel()
			return
		}

		hashed.Add(1)
	}

	persistErr <- nil
}
