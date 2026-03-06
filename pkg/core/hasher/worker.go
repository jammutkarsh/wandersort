package hasher

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/internal/jobtypes"
	"github.com/riverqueue/river"
)

// HashTaskWorker processes file hashing jobs.
type HashTaskWorker struct {
	river.WorkerDefaults[jobtypes.HashTaskArgs]
	Hasher *Hasher
}

// Work processes a single hash job.
func (w *HashTaskWorker) Work(ctx context.Context, job *river.Job[jobtypes.HashTaskArgs]) error {
	args := job.Args

	w.Hasher.log.Info("Hash job started",
		"job_id", job.ID,
		"session_id", args.SessionID,
		"file_id", args.FileID,
		"path", args.FilePath)

	// Hash the file and update database
	if err := w.Hasher.ProcessFile(ctx, args.FileID, args.FilePath); err != nil {
		w.Hasher.log.Error("Hash job failed",
			"job_id", job.ID,
			"file_id", args.FileID,
			"error", err)
		return fmt.Errorf("failed to process file: %w", err)
	}

	// Update scan session progress
	sessionID, err := uuid.Parse(args.SessionID)
	if err == nil {
		_, err = w.Hasher.db.Exec(ctx, `
			UPDATE scan_sessions 
			SET files_hashed = files_hashed + 1 
			WHERE id = $1
		`, sessionID)

		if err != nil {
			w.Hasher.log.Warn("Failed to update scan session progress",
				"session_id", args.SessionID,
				"error", err)
		}
	}

	w.Hasher.log.Info("Hash job completed",
		"job_id", job.ID,
		"file_id", args.FileID)

	return nil
}
