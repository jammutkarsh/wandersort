package exiftool

import (
	"time"

	"github.com/jammutkarsh/wandersort/pkg/core/classifier"
)

// Config controls how the Extractor runs.
type Config struct {
	// Workers is the number of concurrent exiftool processes. Defaults to 4.
	Workers int
	// Timeout is the per-file deadline passed to exec.CommandContext.
	// Zero means no timeout.
	Timeout time.Duration
}

func (c *Config) setDefaults() {
	if c.Workers <= 0 {
		c.Workers = 4
	}
}

// Result holds the outcome for a single file.
type Result struct {
	// SourceFile is the absolute path that was processed.
	SourceFile string
	// Common is the normalised, all-string metadata for the file.
	// Fields absent for the given file type are left as "".
	Common classifier.CommonMetadata
	// Err is non-nil when extraction or parsing failed for this file.
	Err error
}

// job is the internal unit of work dispatched to each worker.
type job struct {
	path string
}
