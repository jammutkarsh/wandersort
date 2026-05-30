package statusmanager

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

const (
	WorkflowStatusStarted   = "STARTED"
	WorkflowStatusScan      = "SCAN"
	WorkflowStatusHash      = "HASH"
	WorkflowStatusScore     = "SCORE"
	WorkflowStatusFail      = "FAILED"
	WorkflowStatusCancelled = "CANCELLED"
)

type WorkflowStatus struct {
	SessionID       uuid.UUID `json:"sessionId"`
	Status          string    `json:"status"` // SCAN, HASH, SCORE, FAILED, CANCELLED
	FilesDiscovered int64     `json:"filesDiscovered"`
	FilesSkipped    int64     `json:"filesSkipped"`
	FilesNew        int64     `json:"filesNew"`
	FilesHashed     int64     `json:"filesHashed"`
	Errors          int64     `json:"errors"`
	LastError       string    `json:"lastError,omitempty"`
}

// SessionTracker tracks the progress of a multi-directory scan/hash/score session.
type SessionTracker struct {
	SessionID uuid.UUID

	Status atomic.Value // Stores string (e.g. status.WorkflowStatusScan)

	Discovered atomic.Int64
	Skipped    atomic.Int64
	NewFiles   atomic.Int64
	Modified   atomic.Int64
	Hashed     atomic.Int64
	Errors     atomic.Int64

	Unsupported      atomic.Int64
	UnsupportedMu    sync.Mutex
	UnsupportedPaths []string

	PendingJobs atomic.Int32
	Ctx         context.Context
	Cancel      context.CancelFunc
}
