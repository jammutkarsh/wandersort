package statusmanager

import (
	"context"
	"sort"
	"strings"
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

// Tracker tracks the progress of a multi-directory scan/hash/score session.
type Tracker struct {
	SessionID uuid.UUID

	Status atomic.Value // Stores string (e.g. status.WorkflowStatusScan)

	Discovered       atomic.Int64
	Skipped          atomic.Int64
	NewFiles         atomic.Int64
	Modified         atomic.Int64
	Hashed           atomic.Int64
	Errors           atomic.Int64
	Unsupported      atomic.Int64
	UnsupportedPaths atomic.Value // Stores comma-separated string

	PendingJobs atomic.Int32
	Ctx         context.Context
	Cancel      context.CancelFunc
}

// AddUnsupportedPath adds a path to the list of unsupported files in a thread-safe way.
func (t *Tracker) AddUnsupportedPath(path string) {
	for {
		v := t.UnsupportedPaths.Load()
		oldStr := ""
		if v != nil {
			oldStr = v.(string)
		}

		newStr := path
		if oldStr != "" {
			newStr = oldStr + "," + path
		}

		if t.UnsupportedPaths.CompareAndSwap(v, newStr) {
			t.Unsupported.Add(1)
			return
		}
	}
}

// GetUnsupportedPaths returns a sorted slice of the unsupported paths.
func (t *Tracker) GetUnsupportedPaths() []string {
	v := t.UnsupportedPaths.Load()
	if v == nil {
		return nil
	}
	s := v.(string)
	if s == "" {
		return nil
	}
	paths := strings.Split(s, ",")
	sort.Strings(paths)
	return paths
}
