package hasher

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	sm "github.com/jammutkarsh/wandersort/pkg/statusmanager"
	"lukechampine.com/blake3"
)

// Hasher handles file hashing and content group management
type Hasher struct {
	ctx       context.Context
	db        *db.DB
	log       logger.Logger
	statusMgr *sm.StatusManager
	path      *path.Resolver
}

// NewHasher creates a new hasher instance
func NewHasher(ctx context.Context, db *db.DB, log logger.Logger, sm *sm.StatusManager) *Hasher {

	return &Hasher{
		ctx:       ctx,
		db:        db,
		log:       log,
		statusMgr: sm,
		path:      path.New(),
	}
}

// Run fetches hashable files for the given session in pages and executes
// hashing in bounded worker pools.
func (h *Hasher) Run(ctx context.Context, tracker *sm.Tracker, workerCount int) (int, error) {
	queueSize := max(workerCount*2, 2)

	toHash := make(chan fileRecord, queueSize)    // file to hash
	toStore := make(chan hashedRecord, queueSize) // hash to DB
	producerErr := make(chan error, 1)
	hasherErr := make(chan error, 1)

	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	h.log.Info("Hashing session", "session_id", tracker.SessionID)
	sessionStr := tracker.SessionID.String()
	startHashed := tracker.Hashed.Load()

	go h.producer(ctxWithCancel, cancel, sessionStr, toHash, producerErr)
	go h.hasher(ctxWithCancel, cancel, workerCount, toHash, toStore, tracker)
	go h.store(ctxWithCancel, cancel, toStore, tracker, hasherErr)

	total := int(tracker.Hashed.Load() - startHashed)

	if err := <-producerErr; err != nil {
		return total, err
	}
	if err := <-hasherErr; err != nil {
		return total, err
	}

	if _, err := h.db.ExecContext(ctx, `
		UPDATE scan_sessions
		SET files_hashed = COALESCE(files_hashed, 0) + ?
		WHERE id = ?`, total, sessionStr); err != nil {
		return total, fmt.Errorf("failed to update files_hashed counter: %w", err)
	}

	h.log.Info("Hashing complete", "session_id", tracker.SessionID, "files_hashed", total)
	return total, nil
}

func (h *Hasher) producer(ctx context.Context, cancel context.CancelFunc, sessionStr string, toHash chan<- fileRecord, producerErr chan<- error) {
	defer close(toHash)

	for {
		record, ok, err := h.getFile(ctx, sessionStr)
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

func (h *Hasher) getFile(ctx context.Context, sessionStr string) (fileRecord, bool, error) {
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
		QueryRowContext(ctx, query, scanner.ScanStatusHashing, sessionStr, scanner.ScanStatusDiscovered).
		Scan(&id, &filePath, &sourceRoot)
	if errors.Is(err, sql.ErrNoRows) {
		return fileRecord{}, false, nil
	}
	if err != nil {
		return fileRecord{}, false, fmt.Errorf("claim next hash row: %w", err)
	}

	return fileRecord{id: id, absPath: h.path.MakeAbsolute(filePath, sourceRoot)}, true, nil
}

func (h *Hasher) hasher(ctx context.Context, cancel context.CancelFunc, workerCount int, toHash <-chan fileRecord, toPersist chan<- hashedRecord, tracker *sm.Tracker) {
	var hashWG sync.WaitGroup

	for range workerCount {
		hashWG.Go(func() {
			for file := range toHash {
				if ctx.Err() != nil {
					return
				}

				hash, err := h.hashFile(file.absPath)
				if err != nil {
					h.log.Error("Failed to hash file", "file_id", file.id, "path", file.absPath, "error", err)
					h.markFileError(file.id)
					if tracker != nil {
						tracker.Errors.Add(1)
					}
					continue
				}

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

	hasher := blake3.New(32, nil)

	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}

	sum := make([]byte, 0, 32)
	hash := hex.EncodeToString(hasher.Sum(sum))
	return hash, nil
}

func (h *Hasher) store(ctx context.Context, cancel context.CancelFunc, toPersist <-chan hashedRecord, tracker *sm.Tracker, persistErr chan<- error) {
	for hashed := range toPersist {
		select {
		case <-ctx.Done():
			persistErr <- ctx.Err()
			cancel()
			return
		default:
		}

		ok := h.db.Writer.Write(func(ctx context.Context, tx *sql.Tx) error {
			return h.storeHash(ctx, tx, hashed.id, hashed.hash)
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

		if tracker != nil {
			tracker.Hashed.Add(1)
		}
	}

	h.db.Writer.Flush()
	persistErr <- nil
}

func (h *Hasher) storeHash(ctx context.Context, tx *sql.Tx, fileID int64, hash string) error {
	// Update Hash and Status
	if _, err := tx.ExecContext(ctx, `
		UPDATE file_registry
		SET file_hash = ?, scan_status = 'HASHED'
		WHERE id = ?
	`, hash, fileID); err != nil {
		return fmt.Errorf("failed to update file registry: %w", err)
	}

	// Upsert Content Group and Membership
	var groupID int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO content_groups (content_hash, total_copies)
		VALUES (?, 1)
		ON CONFLICT (content_hash)
		DO UPDATE SET total_copies = content_groups.total_copies + 1
		RETURNING id
	`, hash).Scan(&groupID)
	if err != nil {
		return fmt.Errorf("failed to upsert/fetch content group: %w", err)
	}

	// Insert membership (idempotent)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO content_group_members (group_id, file_id, is_master, metadata_score)
		VALUES (?, ?, 0, 0)
		ON CONFLICT (group_id, file_id) DO NOTHING
	`, groupID, fileID); err != nil {
		return fmt.Errorf("failed to add member to group: %w", err)
	}

	return nil
}

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
