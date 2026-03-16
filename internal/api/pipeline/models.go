// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package pipeline

// StartScanRequest is the body for POST /pipeline/start.
type StartScanRequest struct {
	RootPaths []string `json:"rootPaths" binding:"required"`
}

// StartScanResponse is returned after a scan is successfully submitted.
type StartScanResponse struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

// FileCountResponse contains the combined file counts for the whole pipeline.
type FileCountResponse struct {
	FilesScanned int64 `json:"filesScanned"`
	FilesHashed  int64 `json:"filesHashed"`
}
