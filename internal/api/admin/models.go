// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

// ResetResponse reports how many rows were deleted from each table
type ResetResponse struct {
	VFSEntriesDeleted   int64 `json:"vfsEntriesDeleted"`
	FileMetadataDeleted int64 `json:"fileMetadataDeleted"`
	FilesDeleted        int64 `json:"filesDeleted"`
	ScanSessionsDeleted int64 `json:"scanSessionsDeleted"`
	UserLabelsDeleted   int64 `json:"userLabelsDeleted"`
}
