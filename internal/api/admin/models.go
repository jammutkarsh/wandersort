package admin

// ResetResponse reports how many rows were deleted from each table.
type ResetResponse struct {
	GroupMembersDeleted  int64 `json:"groupMembersDeleted"`
	ContentGroupsDeleted int64 `json:"contentGroupsDeleted"`
	FilesDeleted         int64 `json:"filesDeleted"`
	ScanSessionsDeleted  int64 `json:"scanSessionsDeleted"`
}
