// Package jobtypes defines shared River job argument types used across
// multiple packages (e.g. scanner enqueues hash jobs, hasher processes them).
// Keeping them in a neutral internal package avoids circular imports while
// ensuring both sides stay in sync via the type system.
package jobtypes

import (
	"github.com/jammutkarsh/wandersort/pkg/queue"
	"github.com/riverqueue/river"
)

// HashTaskArgs is the payload for a BLAKE3 file-hashing job.
type HashTaskArgs struct {
	SessionID string `json:"sessionId"`
	FileID    int64  `json:"fileId"`
	FilePath  string `json:"filePath"`
}

// Kind returns the River job-type discriminator.
func (HashTaskArgs) Kind() string { return "hash_task" }

// InsertOpts routes hash jobs to the dedicated hash queue.
func (HashTaskArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.HashQueue}
}
