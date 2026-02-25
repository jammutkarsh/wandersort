package hasher

import (
	"time"
)

// ContentGroup represents a group of files with identical content (hash)
type ContentGroup struct {
	ID           int64     `json:"id"`
	ContentHash  string    `json:"contentHash"`
	MasterFileID *int64    `json:"masterFileId,omitempty"`
	TotalCopies  int       `json:"totalCopies"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// ContentGroupMember represents a file's membership in a content group
type ContentGroupMember struct {
	ID            int64     `json:"id"`
	GroupID       int64     `json:"groupId"`
	FileID        int64     `json:"fileId"`
	IsMaster      bool      `json:"isMaster"`
	MetadataScore int       `json:"metadataScore"`
	CreatedAt     time.Time `json:"createdAt"`
}

// ScoringCriteria defines what makes a file a good master candidate
type ScoringCriteria struct {
	HasEXIF          bool // +10 points
	HasDatePattern   bool // +5 points (filename like 20230520_...)
	HasMeaningfulDir bool // +2 points (not "New Folder", "DCIM", etc.)
}
