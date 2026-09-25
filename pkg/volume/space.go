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
	// One file per content hash: duplicates are never copied, so summing every
	// live file would overstate what a scan writes, sometimes double so. This
	// counts by hash rather than by the elected master of each group, because
	// identical bytes are identical sizes — *which* copy wins the election is
	// a question this package has no business asking, and asking it would mean
	// reaching up into pkg/core for a rule that lives there.
	var librarySize int64
	if err := database.SQL.GetContext(ctx, &librarySize,
		`SELECT COALESCE(SUM(size), 0) FROM (
			SELECT MIN(fr.file_size) AS size FROM file_registry fr
			JOIN file_metadata fm ON fm.file_id = fr.id
			GROUP BY fm.file_hash)`); err != nil {
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

// FreeBytes returns the bytes available to the current user on the volume
// containing path.
func FreeBytes(path string) (uint64, error) {
	free, _, err := Space(path)
	return free, err
}

// minReserve and reserveShare size the room a transfer leaves free: 1 GiB,
// or 1% of the volume when that is more. A disk filled to its last byte
// breaks the database's own next write, and the OS and every other program
// on that volume with it.
const (
	minReserve   = 1 << 30
	reserveShare = 100 // 1/100 of the volume
)

// TransferNeeds is the free space a transfer of fileBytes needs on a volume
// of total bytes, for a database of dbBytes. The backup is written first:
// its uncompressed copy and the compressed file sit on disk together for a
// moment, and neither is bigger than the database, so 2 × dbBytes covers it.
// Each file's temp copy becomes the file itself, so files count once.
func TransferNeeds(fileBytes, dbBytes, total uint64) uint64 {
	return fileBytes + 2*dbBytes + max(minReserve, total/reserveShare)
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
