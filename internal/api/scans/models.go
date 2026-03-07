package scans

import (
	"time"

	"github.com/google/uuid"
)

type StartScanRequest struct {
	RootPaths []string `json:"rootPaths" binding:"required"`
}

type ScanStatusRequest struct {
	SessionID string `form:"sessionId" binding:"required"`
}

type StartScanResponse struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

type FileCountResponse struct {
	TotalFiles int64 `json:"totalFiles"`
}

type CleanupOutputResponse struct {
	DeletedCount int64  `json:"deletedCount"`
	Message      string `json:"message"`
}

// ScanSession mirrors the core representation of a scan session for the API layer.
type ScanSession struct {
	ID          uuid.UUID  `json:"id"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Status      string     `json:"status"`

	RootPaths []string `json:"root_paths"`

	FilesDiscovered int `json:"files_discovered"`
	FilesSkipped    int `json:"files_skipped"`
	FilesNew        int `json:"files_new"`
	FilesModified   int `json:"files_modified"`

	ErrorsEncountered int     `json:"errors_encountered"`
	LastError         *string `json:"last_error,omitempty"`
}
