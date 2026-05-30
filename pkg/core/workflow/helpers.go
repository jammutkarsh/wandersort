package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/statusmanager"
)

/*-------------------- EXPORTED FUNCTION --------------------*/

// StatusStream returns a new channel subscribed to pipeline progress updates.
func (p *Workflow) StatusStream() chan statusmanager.WorkflowStatus {
	return p.statusMgr.Subscribe()
}

// UnsubscribeStatus removes a subscriber from progress updates.
func (p *Workflow) UnsubscribeStatus(ch chan statusmanager.WorkflowStatus) {
	p.statusMgr.Unsubscribe(ch)
}

// Close gracefully waits for all in-flight sessions to finish.
// Call this before closing the database to prevent panics.
func (p *Workflow) Close() {
	p.wg.Wait()
}

/*-------------------- STATUS UPDATES --------------------*/

func (p *Workflow) finalizeSession(sessionID uuid.UUID, finalStatus string, finalErr *string) {
	// Root context for finalization should be independent of session cancellation
	// but bound by a safety timeout.
	finalizeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	v, ok := p.activeSessions.LoadAndDelete(sessionID)
	if !ok {
		p.log.Warn("finalizeSession called for unknown session", "session_id", sessionID)
		completedAt := time.Now().UTC().Format(time.RFC3339)
		_, err := p.db.ExecRetry(finalizeCtx, `
			UPDATE scan_sessions
			SET completed_at = ?, status = ?, last_error = ?
			WHERE id = ?
		`, completedAt, finalStatus, finalErr, sessionID.String())
		if err != nil {
			p.log.Error("Failed to finalize session without tracker", "session_id", sessionID, "error", err)
		}
		return
	}
	tracker := v.(*statusmanager.SessionTracker)

	// Stop progress updater and clear resources.
	tracker.Cancel()

	p.log.Info("Completing pipeline session", "session_id", sessionID, "status", finalStatus)
	completedAt := time.Now().UTC().Format(time.RFC3339)

	_, err := p.db.ExecRetry(finalizeCtx, `
		UPDATE scan_sessions
		SET completed_at = ?,
			status = ?,
			files_discovered = ?,
			files_skipped = ?,
			files_new = ?,
			files_modified = ?,
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
		tracker.Errors.Load(),
		finalErr,
		sessionID.String(),
	)

	if err != nil {
		p.log.Error("Failed to update final session state", "session_id", sessionID, "error", err)
	}

	var filesHashed int64
	if err := p.db.QueryRowContext(finalizeCtx, `SELECT COALESCE(files_hashed, 0) FROM scan_sessions WHERE id = ?`, sessionID.String()).Scan(&filesHashed); err != nil {
		p.log.Warn("Failed to read files_hashed while broadcasting final status", "session_id", sessionID, "error", err)
	}
	p.statusMgr.Broadcast(statusmanager.WorkflowStatus{
		SessionID:       sessionID,
		Status:          finalStatus,
		FilesDiscovered: tracker.Discovered.Load(),
		FilesSkipped:    tracker.Skipped.Load(),
		FilesNew:        tracker.NewFiles.Load(),
		FilesHashed:     filesHashed,
		Errors:          tracker.Errors.Load(),
	})

	p.log.Info("Pipeline session finished", "session_id", sessionID, "status", finalStatus)
}

// setSessionStatus updates the current phase/status for a scan session.
func (p *Workflow) setSessionStatus(ctx context.Context, sessionID uuid.UUID, statusValue string) error {
	_, err := p.db.ExecRetry(ctx, `
		UPDATE scan_sessions
		SET status = ?
		WHERE id = ?
	`, statusValue, sessionID.String())
	if err != nil {
		return fmt.Errorf("set session status: %w", err)
	}

	if v, ok := p.activeSessions.Load(sessionID); ok {
		if tracker, ok := v.(*statusmanager.SessionTracker); ok {
			tracker.Status.Store(statusValue)
		}
	}

	if p.statusMgr != nil {
		current := p.statusMgr.GetCurrent()
		if current.SessionID != sessionID {
			current = statusmanager.WorkflowStatus{SessionID: sessionID}
		}
		current.Status = statusValue
		p.statusMgr.Broadcast(current)
	}
	return nil
}

// writeUnsupportedFiles writes all paths with unsupported extensions that were
// collected during the scan to <outputPath>/unsupported_files_<sessionID>.txt,
// one human-readable (home-contracted) path per line, sorted alphabetically.
// No file is created when every scanned file had a recognised extension.
func (p *Workflow) writeUnsupportedFiles(sessionID uuid.UUID, tracker *statusmanager.SessionTracker) {
	tracker.UnsupportedMu.Lock()
	paths := make([]string, len(tracker.UnsupportedPaths))
	copy(paths, tracker.UnsupportedPaths)
	tracker.UnsupportedMu.Unlock()

	if len(paths) == 0 {
		p.log.Debug("No unsupported files found; skipping report")
		return
	}

	sort.Strings(paths)

	if err := os.MkdirAll(p.outputPath, 0o755); err != nil {
		p.log.Error("Failed to create output directory for unsupported report", "error", err)
		return
	}

	reportPath := filepath.Join(p.outputPath, fmt.Sprintf("unsupported_files_%s.txt", sessionID))
	f, err := os.Create(reportPath)
	if err != nil {
		p.log.Error("Failed to create unsupported files report", "path", reportPath, "error", err)
		return
	}
	defer f.Close()

	header := fmt.Sprintf(
		"# Unsupported files found during scan %s\n"+
			"# These file types are not yet supported by WanderSort.\n"+
			"# Please raise a feature request at https://github.com/jammutkarsh/wandersort/issues\n\n",
		sessionID,
	)
	if _, err := fmt.Fprint(f, header); err != nil {
		p.log.Error("Failed to write report header", "error", err)
		return
	}

	for _, path := range paths {
		if _, err := fmt.Fprintln(f, path); err != nil {
			p.log.Error("Failed to write path to report", "path", path, "error", err)
			return
		}
	}

	// Flush to disk to ensure the report survives unexpected termination.
	if err := f.Sync(); err != nil {
		p.log.Error("Failed to sync unsupported files report", "error", err)
	}

	p.log.Info("Unsupported files report written", "path", reportPath, "count", len(paths))
}

// updateProgress periodically syncs in-memory counters to the database and broadcasts status.
func (p *Workflow) updateProgress(ctx context.Context, sessionID uuid.UUID, tracker *statusmanager.SessionTracker) {
	ticker := time.NewTicker(5 * time.Second) // Default interval
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := p.db.ExecRetry(ctx, `
					UPDATE scan_sessions
					SET files_discovered = ?,
						files_skipped = ?,
						files_new = ?,
						files_modified = ?,
						files_hashed = ?,
						errors_encountered = ?
					WHERE id = ?
			`,
				tracker.Discovered.Load(),
				tracker.Skipped.Load(),
				tracker.NewFiles.Load(),
				tracker.Modified.Load(),
				tracker.Hashed.Load(),
				tracker.Errors.Load(),
				sessionID.String(),
			)

			if p.statusMgr != nil {
				currentStatus, _ := tracker.Status.Load().(string)
				p.statusMgr.Broadcast(statusmanager.WorkflowStatus{
					SessionID:       sessionID,
					Status:          currentStatus,
					FilesDiscovered: tracker.Discovered.Load(),
					FilesSkipped:    tracker.Skipped.Load(),
					FilesNew:        tracker.NewFiles.Load(),
					FilesHashed:     tracker.Hashed.Load(),
					Errors:          tracker.Errors.Load(),
				})
			}

			if err != nil {
				p.log.Warn("Failed to update progress", "error", err)
			}
		}
	}
}
