package hasher

import (
	"time"
)

// ContentGroup represents a group of files with identical content (hash)
type ContentGroup struct {
	ID           int64     `json:"id"`
	ContentHash  string    `json:"content_hash"`
	MasterFileID *int64    `json:"master_file_id,omitempty"`
	TotalCopies  int       `json:"total_copies"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ContentGroupMember represents a file's membership in a content group
type ContentGroupMember struct {
	ID            int64     `json:"id"`
	GroupID       int64     `json:"group_id"`
	FileID        int64     `json:"file_id"`
	IsMaster      bool      `json:"is_master"`
	MetadataScore int       `json:"metadata_score"`
	CreatedAt     time.Time `json:"created_at"`
}

// ScoringCriteria defines what makes a file a good master candidate
type ScoringCriteria struct {
	HasEXIF          bool // +10 points
	HasDatePattern   bool // +5 points (filename like 20230520_...)
	HasMeaningfulDir bool // +2 points (not "New Folder", "DCIM", etc.)
}
