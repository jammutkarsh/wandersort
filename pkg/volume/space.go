// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package volume

import (
	"context"
	"fmt"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// CheckOutputSpace warns, once, when the output volume cannot hold the whole
// library. Best-effort: an unreadable size or volume is a warning, never a
// failure. Lives next to FreeBytes rather than in the pipeline, so the review
// TUI can run the same check without importing the orchestrator.
func CheckOutputSpace(ctx context.Context, database *db.DB, log logger.Logger, outputDir string) {
	// Only master files ever get a target — duplicates are never copied — so
	// summing every live file (including duplicates) overstates what a scan
	// will actually write, sometimes double so.
	var librarySize int64
	if err := database.SQL.GetContext(ctx, &librarySize,
		`SELECT COALESCE(SUM(fr.file_size), 0) FROM file_registry fr
		 JOIN file_metadata fm ON fm.file_id = fr.id
		 WHERE fm.is_master = 1`); err != nil {
		log.Error("Failed to size the library", "error", err)
		return
	}

	free, err := FreeBytes(outputDir)
	if err != nil {
		log.Warn("Cannot check output volume free space", "path", outputDir, "error", err)
		return
	}

	if uint64(librarySize) > free {
		msg := fmt.Sprintf("Output volume may be too small: organizing the library needs up to %s, but only %s is free at %s",
			HumanBytes(uint64(librarySize)), HumanBytes(free), outputDir)
		log.Warn(msg, logger.UserKey, true)
	}
}

// HumanBytes renders n as a short base-1024 size, e.g. "1.5 GiB"
func HumanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
