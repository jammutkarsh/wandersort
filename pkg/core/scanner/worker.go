package scanner

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// ScanTaskArgs is the River job payload for a single scan request.
// One job is inserted per StartScan call; the worker drives the full walk.
type ScanTaskArgs struct {
	SessionID     string   `json:"sessionId"`
	OriginalPaths []string `json:"originalPaths"` // as supplied by the user (may contain ~)
	ExpandedRoots []string `json:"expandedRoots"` // absolute, pre-validated by prepareSession
}

// Kind is the unique job-type identifier River uses for routing.
func (ScanTaskArgs) Kind() string {
	return "scan_task"
}

// InsertOpts routes all scan jobs to the dedicated scan queue.
func (ScanTaskArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: "file_scanning"}
}

// ScanTaskWorker is the River worker that processes a scan_task job.
type ScanTaskWorker struct {
	river.WorkerDefaults[ScanTaskArgs]
	Scanner *Scanner
}

// Work is called by River when a scan_task job is dequeued.
func (w *ScanTaskWorker) Work(ctx context.Context, job *river.Job[ScanTaskArgs]) error {
	sessionID, err := uuid.Parse(job.Args.SessionID)
	if err != nil {
		return fmt.Errorf("invalid session_id in job %d: %w", job.ID, err)
	}

	session := &ScanSession{
		ID:        sessionID,
		StartedAt: time.Now(),
		Status:    ScanStatusRunning,
		RootPaths: job.Args.OriginalPaths,
	}

	w.Scanner.executeScan(ctx, session, job.Args.ExpandedRoots)
	return nil
}
