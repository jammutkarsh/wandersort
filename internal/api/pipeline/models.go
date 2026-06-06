package pipeline

// StartScanRequest is the body for POST /pipeline/start.
type StartScanRequest struct {
	RootPaths []string `json:"rootPaths" binding:"required"`
}

// StartScanResponse is returned after a scan is successfully submitted.
type StartScanResponse struct {
	SessionID string   `json:"sessionId"`
	Status    string   `json:"status"`
	Message   string   `json:"message"`
	ScanPaths []string `json:"scanPaths"`
}

// FileCountResponse contains the combined file counts for the whole pipeline.
type FileCountResponse struct {
	FilesScanned int64 `json:"filesScanned"`
	FilesHashed  int64 `json:"filesHashed"`
}
