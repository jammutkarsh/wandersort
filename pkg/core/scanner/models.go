package scanner

import (
	"time"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/core/classifier"
)

type FileRegistry struct {
	ID             int64     `db:"id"`
	FilePath       string    `db:"file_path"`
	FileSize       int64     `db:"file_size"`
	FileModifiedAt time.Time `db:"file_modified_at"`
	FileHash       *string   `db:"file_hash"`

	DiscoveredAt  time.Time `db:"discovered_at"`
	LastSeenAt    time.Time `db:"last_seen_at"`
	ScanSessionID uuid.UUID `db:"scan_session_id"`
	SourceRoot    string    `db:"source_root"`

	MediaType     string `db:"media_type"`
	FileExtension string `db:"file_extension"`
	ScanStatus    string `db:"scan_status"`

	// Path storage
	PathType   string `db:"path_type" json:"pathType"`     // RELATIVE or ABSOLUTE
	FileOrigin string `db:"file_origin" json:"fileOrigin"` // SOURCE or ORGANIZED

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Path type constants
const (
	PathTypeRelative = "RELATIVE"
	PathTypeAbsolute = "ABSOLUTE"
)

// File origin constants
const (
	FileOriginSource    = "SOURCE"
	FileOriginOrganized = "ORGANIZED"
	FileOriginUnknown   = "UNKNOWN"
)

const (
	ScanStatusDiscovered = "DISCOVERED"
	ScanStatusHashing    = "HASHING"
	ScanStatusHashed     = "HASHED"
	ScanStatusAnalyzing  = "ANALYZING"
	ScanStatusAnalyzed   = "ANALYZED"
	ScanStatusError      = "ERROR"
)

// GetAbsolutePath returns the full absolute path, expanding relative paths using source root.
func (fr *FileRegistry) GetAbsolutePath(pathUtil *PathUtil) string {
	if fr.PathType == PathTypeAbsolute {
		return fr.FilePath
	}
	return pathUtil.MakeAbsolute(fr.FilePath, fr.SourceRoot)
}

// IsPrimarySource reports whether this registry entry is an original/canonical file.
// RAW files from a DSLR that has no paired JPG are still primary sources.
func (fr *FileRegistry) IsPrimarySource() bool {
	switch fr.MediaType {
	case classifier.MediaTypeImage, classifier.MediaTypeRaw, classifier.MediaTypeVideo:
		return true
	default:
		return false
	}
}

// NeedsTranscoding reports whether this file must be decoded on the fly before
// being passed to downstream consumers such as AI inference pipelines.
// RAW images cannot be used directly and must be converted first.
func (fr *FileRegistry) NeedsTranscoding() bool {
	return fr.MediaType == classifier.MediaTypeRaw
}

// FileDiscovery is the lightweight struct used during directory walking
type FileDiscovery struct {
	Path       string
	Size       int64
	ModTime    time.Time
	Extension  string
	SourceRoot string
	MediaType  string
}

type ScanSession struct {
	ID          uuid.UUID  `db:"id"`
	StartedAt   time.Time  `db:"started_at"`
	CompletedAt *time.Time `db:"completed_at"`
	Status      string     `db:"status"`

	RootPaths []string `db:"root_paths"` // JSONB in DB

	FilesDiscovered int `db:"files_discovered"`
	FilesSkipped    int `db:"files_skipped"`
	FilesNew        int `db:"files_new"`
	FilesModified   int `db:"files_modified"`

	ErrorsEncountered int     `db:"errors_encountered"`
	LastError         *string `db:"last_error"`
}

const (
	ScanStatusRunning   = "RUNNING"
	ScanStatusCompleted = "COMPLETED"
	ScanStatusFailed    = "FAILED"
	ScanStatusCancelled = "CANCELLED"
)
