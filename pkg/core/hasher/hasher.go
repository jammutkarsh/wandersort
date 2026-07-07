package hasher

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"lukechampine.com/blake3"
)

const (
	// hashOutputSize is the output length of BLAKE3-256 in bytes
	hashOutputSize = 32
)

// Hasher handles file hashing and content group management
type Hasher struct {
	ctx      context.Context
	db       *db.DB
	log      logger.Logger
	path     *path.Resolver
	exiftool *exiftool.Extractor
	workers  int
}

func New(ctx context.Context, db *db.DB, log logger.Logger, exiftoolPath string, workers int) *Hasher {
	return &Hasher{
		ctx:      ctx,
		db:       db,
		log:      log,
		path:     path.New(),
		exiftool: exiftool.New(exiftoolPath),
		workers:  workers,
	}
}

// Run fetches hashable files for the given session in pages and executes
// hashing in bounded worker pools
func (h *Hasher) Run(ctx context.Context, sessionID uuid.UUID) (int, error) {
	toHash := make(chan fileRecord, 2*h.workers)
	toStore := make(chan hashedRecord, 2*h.workers)
	producerErr := make(chan error, 1)
	hasherErr := make(chan error, 1)

	var hashedCount atomic.Int64
	var errorCount atomic.Int64

	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	h.log.Info("Hashing session", "sessionId", sessionID)

	go h.producer(ctxWithCancel, sessionID, cancel, toHash, producerErr)
	go h.hasher(ctxWithCancel, sessionID, cancel, toHash, toStore, &errorCount)
	go h.store(ctxWithCancel, cancel, toStore, &hashedCount, hasherErr)

	if err := <-producerErr; err != nil {
		return 0, err
	}
	if err := <-hasherErr; err != nil {
		return 0, err
	}

	total := int(hashedCount.Load())
	errorTotal := errorCount.Load()
	if _, upErr := h.db.ExecContext(ctx, `
		UPDATE scan_sessions
		SET files_hashed = ?, errors_encountered = errors_encountered + ?
		WHERE id = ?
	`, total, errorTotal, sessionID.String()); upErr != nil {
		h.log.Error("Failed to update hash counters", "sessionId", sessionID, "error", upErr)
	}

	h.log.Info("Hashing complete", "sessionId", sessionID, "filesHashed", total)
	return total, nil
}

// producer fetches hashable files from the database and feeds them into the toHash channel
func (h *Hasher) producer(ctx context.Context, sessionID uuid.UUID, cancel context.CancelFunc, toHash chan<- fileRecord, producerErr chan<- error) {
	defer close(toHash)

	for {
		record, ok, err := h.getFile(ctx, sessionID)
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
func (h *Hasher) getFile(ctx context.Context, sessionID uuid.UUID) (fileRecord, bool, error) {
	var id int64
	var filePath, sourceRoot string
	query := `
	UPDATE file_registry
	SET scan_status = ?
	WHERE id = (
		SELECT id
		FROM file_registry
		WHERE scan_session_id = ?
			AND scan_status = ?
		ORDER BY id
		LIMIT 1
	)
	RETURNING id, file_path, source_root`

	err := h.db.
		QueryRowContext(ctx, query, db.StatusHashing, sessionID.String(), db.StatusDiscovered).
		Scan(&id, &filePath, &sourceRoot)
	if errors.Is(err, sql.ErrNoRows) {
		return fileRecord{}, false, nil
	}
	if err != nil {
		return fileRecord{}, false, fmt.Errorf("claim next hash row: %w", err)
	}

	return fileRecord{id: id, absPath: h.path.MakeAbsolute(filePath, sourceRoot)}, true, nil
}

// hasher runs the bounded worker pool that computes BLAKE3 hashes and extracts EXIF
func (h *Hasher) hasher(ctx context.Context, sessionID uuid.UUID, cancel context.CancelFunc, toHash <-chan fileRecord, toPersist chan<- hashedRecord, errorCount *atomic.Int64) {
	var hashWG sync.WaitGroup

	for range h.workers {
		hashWG.Go(func() {
			for file := range toHash {
				if ctx.Err() != nil {
					return
				}

				hash, err := h.hashFile(file.absPath)
				if err != nil {
					h.log.Error("Failed to hash file", "sessionId", sessionID, "fileId", file.id, "path", file.absPath, "error", err)
					h.markFileError(file.id)
					errorCount.Add(1)
					continue
				}

				exifData, err := h.exiftool.Extract(ctx, file.absPath)
				if err != nil {
					h.log.Warn("Failed to extract exif data", "sessionId", sessionID, "fileId", file.id, "path", file.absPath, "error", err)
				}

				select {
				case toPersist <- hashedRecord{id: file.id, hash: hash, exif: exifData}:
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

		ok := h.db.Writer.Write(func(ctx context.Context, tx *sql.Tx) error {
			return h.storeHash(ctx, tx, file.id, file.hash, file.exif)
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

	h.db.Writer.Flush()
	persistErr <- nil
}

// storeHash updates the file registry with hash, then upserts content group membership
func (h *Hasher) storeHash(ctx context.Context, tx *sql.Tx, fileID int64, hash string, exif classifier.CommonMetadata) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE file_registry
		SET file_hash = ?, scan_status = 'HASHED'
		WHERE id = ?
	`, hash, fileID); err != nil {
		return fmt.Errorf("failed to update file registry: %w", err)
	}

	var groupID int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO content_groups (
			content_hash, total_copies,
			exif_image_width, exif_image_height,
			exif_gps_latitude, exif_gps_longitude,
			exif_make, exif_model,
			exif_date_time_original, exif_create_date
		) VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (content_hash)
		DO UPDATE SET total_copies = content_groups.total_copies + 1
		RETURNING id
	`, hash,
		intOrNil(exif.ImageWidth),
		intOrNil(exif.ImageHeight),
		floatOrNil(exif.GPSLatitude),
		floatOrNil(exif.GPSLongitude),
		strOrNil(exif.Make),
		strOrNil(exif.Model),
		strOrNil(exif.DateTimeOriginal),
		strOrNil(exif.CreateDate),
	).Scan(&groupID)
	if err != nil {
		return fmt.Errorf("failed to upsert/fetch content group: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO content_group_members (group_id, file_id, is_master, metadata_score)
		VALUES (?, ?, 0, 0)
		ON CONFLICT (group_id, file_id) DO NOTHING
	`, groupID, fileID); err != nil {
		return fmt.Errorf("failed to add member to group: %w", err)
	}

	return nil
}

func intOrNil(s string) any {
	if s == "" {
		return nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return v
}

func floatOrNil(s string) any {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return v
}

func strOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// markFileError sets the file's scan_status to ERROR
func (h *Hasher) markFileError(fileID int64) {
	h.db.Writer.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE file_registry
			SET scan_status = 'ERROR'
			WHERE id = ?
		`, fileID)
		return err
	})
}
