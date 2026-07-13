package admin

// ResetResponse reports how many rows were deleted from each table
type ResetResponse struct {
	FileMetadataDeleted int64 `json:"fileMetadataDeleted"`
	FilesDeleted        int64 `json:"filesDeleted"`
	ScanSessionsDeleted int64 `json:"scanSessionsDeleted"`
}
