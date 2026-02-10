package scans

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
