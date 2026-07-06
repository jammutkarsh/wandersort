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
)

// Close gracefully waits for all in-flight sessions to finish
// Call this before closing the database to prevent panics
func (wf *Workflow) Close() {
	wf.wg.Wait()
}

/*-------------------- STATUS UPDATES --------------------*/

// finalizeSession writes the terminal status/time/error for a scan session
// It uses a detached context for cancelled sessions and a timeout for normal ones
func (wf *Workflow) finalizeSession(sessionID uuid.UUID, finalStatus string, finalErr *string) {
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
	completedAt := time.Now().UTC().Format(time.RFC3339)

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
