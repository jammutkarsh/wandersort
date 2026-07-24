// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// Close gracefully waits for all in-flight sessions to finish
// Call this before closing the database to prevent panics
func (wf *Workflow) Close() {
	wf.wg.Wait()
}

// warnIfLowSpace preempts a doomed Execute run right after the scan phase:
// if every live file were copied into the output directory today, would the
// volume hold it? Warn-only — nothing is copied yet.
// ponytail: the sum counts every live file, an upper bound; narrow it to
// master copies when the Execute phase lands
func (wf *Workflow) warnIfLowSpace(sessionID uuid.UUID) {
	CheckOutputSpace(wf.ctx, wf.db, wf.log, wf.outputDir, sessionID)
}

// CheckOutputSpace warns, once, when the output volume cannot hold the whole
// library. Exported because `review` needs the same check: it is the last
// place a user looks before approving a plan, and finding out mid-move that
// the disk is too small is far worse than being told beforehand. Best-effort
// throughout — an unreadable size or volume is a warning, never a failure.
func CheckOutputSpace(ctx context.Context, database *db.DB, log logger.Logger, outputDir string, sessionID uuid.UUID) {
	var librarySize int64
	if err := database.SQL.GetContext(ctx, &librarySize,
		`SELECT COALESCE(SUM(file_size), 0) FROM live_files`); err != nil {
		log.Error("Failed to size the library", "sessionId", sessionID, "error", err)
		return
	}

	free, err := volume.FreeBytes(outputDir)
	if err != nil {
		log.Warn("Cannot check output volume free space", "sessionId", sessionID, "path", outputDir, "error", err)
		return
	}

	if uint64(librarySize) > free {
		msg := fmt.Sprintf("Output volume may be too small: organizing the library needs up to %s, but only %s is free at %s",
			humanBytes(uint64(librarySize)), humanBytes(free), outputDir)
		log.Warn(msg, logger.UserKey, true, "sessionId", sessionID)
	}
}

// humanBytes renders n as a short base-1024 size, e.g. "1.5 GiB"
func humanBytes(n uint64) string {
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

/*-------------------- STATUS UPDATES --------------------*/

// finalizeSession writes the terminal status/time/error for a scan session
// It uses a detached context for cancelled sessions and a timeout for normal ones
func (wf *Workflow) finalizeSession(sessionID uuid.UUID, finalStatus string, finalErr *string) {
	wf.releaseRoots(sessionID)

	// We select a context based on whether the pipeline was interrupted
	// 1. If CANCELLED: The session context is already dead; use a detached one for the final write
	// 2. If COMPLETED/FAILED: The pipeline was running without interruption; use the app context
	// 3. If App Shutdown: Falling back to detached even for success to ensure the state is persisted
	finalizeCtx, cancel := context.WithCancel(wf.ctx)

	if finalStatus != db.StatusCancelled && wf.ctx.Err() == nil {
		// Pipeline was running without interruption and app is not shutting down
		finalizeCtx, cancel = context.WithTimeout(wf.ctx, defaultFinalizeTimeout)
	}
	defer cancel()

	wf.log.Info("Completing pipeline session", "sessionId", sessionID, "status", finalStatus)
	completedAt := db.FormatTime(time.Now())

	_, err := wf.db.ExecRetry(finalizeCtx, `
		UPDATE scan_sessions
		SET completed_at = ?, status = ?, last_error = ?
		WHERE id = ?
	`, completedAt, finalStatus, finalErr, sessionID.String())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			wf.log.Error("Finalization timed out", "sessionId", sessionID, "timeout", defaultFinalizeTimeout)
			return
		}
		wf.log.Error("Failed to finalize session", "sessionId", sessionID, "error", err)
		return
	}

	wf.log.Info("Pipeline session finished", "sessionId", sessionID, "status", finalStatus)
}

// setSessionStatus updates the current phase/status for a scan session
func (wf *Workflow) setSessionStatus(ctx context.Context, sessionID uuid.UUID, statusValue string) error {
	_, err := wf.db.ExecRetry(ctx, `
		UPDATE scan_sessions
		SET status = ?
		WHERE id = ?
	`, statusValue, sessionID.String())
	if err != nil {
		return fmt.Errorf("set session status: %w", err)
	}

	return nil
}
