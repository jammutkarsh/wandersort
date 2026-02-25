package hash

import (
	"time"
)

// HashProgressResponse is returned by GET /hash/progress
type HashProgressResponse struct {
	SessionID       string     `json:"sessionId"`
	FilesDiscovered int64      `json:"filesDiscovered"`
	FilesHashed     int64      `json:"filesHashed"`
	FilesErrored    int64      `json:"filesErrored"`
	PercentComplete float64    `json:"percentComplete"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
}

// HashStatsResponse is returned by GET /hash/stats
type HashStatsResponse struct {
	TotalGroups     int `json:"totalGroups"`
	GroupsWithDupes int `json:"groupsWithDupes"`
	TotalFiles      int `json:"totalFiles"`
	DuplicateFiles  int `json:"duplicateFiles"`
	MastersElected  int `json:"mastersElected"`
}

// HashProgressRequest binds the session_id query param
type HashProgressRequest struct {
	SessionID string `form:"session_id" binding:"required"`
}
