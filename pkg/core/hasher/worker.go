package hasher

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/queue"
	"github.com/riverqueue/river"
)

// HashTaskArgs represents a file hashing job
type HashTaskArgs struct {
	SessionID string `json:"sessionId"`
	FileID    int64  `json:"fileId"`
	FilePath  string `json:"filePath"`
}

// Kind returns the job type name for River
func (HashTaskArgs) Kind() string {
	return "hash_task"
}

// InsertOpts routes all hash jobs to the dedicated hash queue.
func (HashTaskArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.HashQueue}
}

// HashTaskWorker processes file hashing jobs
type HashTaskWorker struct {
	river.WorkerDefaults[HashTaskArgs]
	Hasher   *Hasher
	jobQueue queue.Enqueuer
}

// Register adds this worker to the River workers registry
func (w *HashTaskWorker) Register(workers *river.Workers) {
	river.AddWorker(workers, w)
}

// SetEnqueuer injects the job queue enqueuer
func (w *HashTaskWorker) SetEnqueuer(e queue.Enqueuer) {
	w.jobQueue = e
}

// Work processes a single hash job
func (w *HashTaskWorker) Work(ctx context.Context, job *river.Job[HashTaskArgs]) error {
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
