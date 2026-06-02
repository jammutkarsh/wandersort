package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	sm "github.com/jammutkarsh/wandersort/pkg/statusmanager"
)

/*-------------------- EXPORTED FUNCTION --------------------*/

// StatusStream returns a new channel subscribed to pipeline progress updates.
func (wf *Workflow) StatusStream() chan sm.WorkflowStatus {
	return wf.statusMgr.Subscribe()
}

// UnsubscribeStatus removes a subscriber from progress updates.
func (wf *Workflow) UnsubscribeStatus(ch chan sm.WorkflowStatus) {
	wf.statusMgr.Unsubscribe(ch)
}

// Close gracefully waits for all in-flight sessions to finish.
// Call this before closing the database to prevent panics.
func (wf *Workflow) Close() {
	wf.wg.Wait()
}

/*-------------------- STATUS UPDATES --------------------*/

func (wf *Workflow) finalizeSession(sessionID uuid.UUID, finalStatus string, finalErr *string) {
	// We select a context based on whether the pipeline was interrupted.
	// 1. If CANCELLED: The session context is already dead; use a detached one for the final write.
	// 2. If COMPLETED/FAILED: The pipeline was running without interruption; use the app context.
	// 3. If App Shutdown: Falling back to detached even for success to ensure the state is persisted.
	finalizeCtx, cancel := context.WithCancel(wf.ctx)

	if finalStatus != sm.WorkflowStatusCancelled && wf.ctx.Err() == nil {
		// Pipeline was running without interruption and app is not shutting down
		finalizeCtx, cancel = context.WithTimeout(wf.ctx, wf.finalTimeout)
	}
	defer cancel()

	session, ok := wf.activeSessions.LoadAndDelete(sessionID)
	if !ok {
		wf.log.Warn("finalizeSession called for unknown session", "session_id", sessionID)
		completedAt := time.Now().UTC().Format(time.RFC3339)
		_, err := wf.db.ExecRetry(finalizeCtx, `
			UPDATE scan_sessions
			SET completed_at = ?, status = ?, last_error = ?
			WHERE id = ?
		`, completedAt, finalStatus, finalErr, sessionID.String())
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				wf.log.Error("Finalization timed out", "session_id", sessionID, "timeout", wf.finalTimeout)
				return
			}
			wf.log.Error("Failed to finalize session without tracker", "session_id", sessionID, "error", err)
		}
		return
	}
	tracker := session.(*sm.Tracker)

	// Stop progress updater and clear resources.
	tracker.Cancel()

	wf.log.Info("Completing pipeline session", "session_id", sessionID, "status", finalStatus)
	completedAt := time.Now().UTC().Format(time.RFC3339)

	_, err := wf.db.ExecRetry(finalizeCtx, `
		UPDATE scan_sessions
		SET completed_at = ?,
			status = ?,
			files_discovered = ?,
			files_skipped = ?,
			files_new = ?,
			files_modified = ?,
			files_hashed = ?,
			errors_encountered = ?,
			last_error = ?
		WHERE id = ?
	`,
		completedAt,
		finalStatus,
		tracker.Discovered.Load(),
		tracker.Skipped.Load(),
		tracker.NewFiles.Load(),
		tracker.Modified.Load(),
		tracker.Hashed.Load(),
		tracker.Errors.Load(),
		finalErr,
		sessionID.String(),
	)

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			wf.log.Error("Finalization timed out while updating state", "session_id", sessionID, "timeout", wf.finalTimeout)
			return
		}
		wf.log.Error("Failed to update final session state", "session_id", sessionID, "error", err)
		return
	}

	wf.statusMgr.Broadcast(sm.WorkflowStatus{
		SessionID:       sessionID,
		Status:          finalStatus,
		FilesDiscovered: tracker.Discovered.Load(),
		FilesSkipped:    tracker.Skipped.Load(),
		FilesNew:        tracker.NewFiles.Load(),
		FilesHashed:     tracker.Hashed.Load(),
		Errors:          tracker.Errors.Load(),
	})

	wf.log.Info("Pipeline session finished", "session_id", sessionID, "status", finalStatus)
}

// setSessionStatus updates the current phase/status for a scan session.
func (wf *Workflow) setSessionStatus(ctx context.Context, sessionID uuid.UUID, statusValue string) error {
	_, err := wf.db.ExecRetry(ctx, `
		UPDATE scan_sessions
		SET status = ?
		WHERE id = ?
	`, statusValue, sessionID.String())
	if err != nil {
		return fmt.Errorf("set session status: %w", err)
	}

	if session, ok := wf.activeSessions.Load(sessionID); ok {
		if tracker, ok := session.(*sm.Tracker); ok {
			tracker.Status.Store(statusValue)
		}
	}

	// TODO: Do deep review of the code below
	// Why is this being done?
	status := wf.statusMgr.LastStatus()
	if status.SessionID != sessionID {
		status = sm.WorkflowStatus{SessionID: sessionID}
	}
	status.Status = statusValue
	wf.statusMgr.Broadcast(status)
	return nil
}

// writeUnsupportedFiles writes all paths with unsupported extensions that were
// collected during the scan to <outputPath>/unsupported_files_<sessionID>.txt,
// one human-readable (home-contracted) path per line, sorted alphabetically.
// No file is created when every scanned file had a recognised extension.
func (wf *Workflow) writeUnsupportedFiles(sessionID uuid.UUID, tracker *sm.Tracker) {
	paths := tracker.GetUnsupportedPaths()

	if len(paths) == 0 {
		wf.log.Debug("No unsupported files found; skipping report")
		return
	}

	if err := os.MkdirAll(wf.outputPath, 0o755); err != nil {
		wf.log.Error("Failed to create output directory for unsupported report", "error", err)
		return
	}

	reportPath := filepath.Join(wf.outputPath, fmt.Sprintf("unsupported_files_%s.txt", sessionID))
	file, err := os.Create(reportPath)
	if err != nil {
		wf.log.Error("Failed to create unsupported files report", "path", reportPath, "error", err)
		return
	}
	defer file.Close()

	header := fmt.Sprintf(
		"# Unsupported files found during scan %s\n"+
			"# These file types are not yet supported by WanderSort.\n"+
			"# Please raise a support request at https://github.com/jammutkarsh/wandersort/issues\n\n",
		sessionID,
	)
	if _, err := fmt.Fprint(file, header); err != nil {
		wf.log.Error("Failed to write report header", "error", err)
		return
	}

	for _, path := range paths {
		if _, err := fmt.Fprintln(file, path); err != nil {
			wf.log.Error("Failed to write path to report", "path", path, "error", err)
			return
		}
	}

	// Flush to disk to ensure the report survives unexpected termination.
	if err := file.Sync(); err != nil {
		wf.log.Error("Failed to sync unsupported files report", "error", err)
	}

	wf.log.Info("Unsupported files report written", "path", reportPath, "count", len(paths))
}

// updateProgress periodically syncs in-memory counters to the database and broadcasts status.
func (wf *Workflow) updateProgress(ctx context.Context, sessionID uuid.UUID, t *sm.Tracker) {
	ticker := time.NewTicker(wf.updateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := wf.db.ExecRetry(ctx, `
					UPDATE scan_sessions
					SET files_discovered = ?,
						files_skipped = ?,
						files_new = ?,
						files_modified = ?,
						files_hashed = ?,
						errors_encountered = ?
					WHERE id = ?
			`,
				t.Discovered.Load(),
				t.Skipped.Load(),
				t.NewFiles.Load(),
				t.Modified.Load(),
				t.Hashed.Load(),
				t.Errors.Load(),
				sessionID.String(),
			)

			currentStatus, _ := t.Status.Load().(string)
			wf.statusMgr.Broadcast(sm.WorkflowStatus{
				SessionID:       sessionID,
				Status:          currentStatus,
				FilesDiscovered: t.Discovered.Load(),
				FilesSkipped:    t.Skipped.Load(),
				FilesNew:        t.NewFiles.Load(),
				FilesHashed:     t.Hashed.Load(),
				Errors:          t.Errors.Load(),
			})

			if err != nil {
				wf.log.Warn("Failed to update progress", "error", err)
			}
		}
	}
}
