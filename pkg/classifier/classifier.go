package classifier

import (
	"path/filepath"
	"strings"
)

// FileClassifier determines file types and filters
type FileClassifier struct {
	imageExtensions   map[string]bool
	videoExtensions   map[string]bool
	rawExtensions     map[string]bool
	sidecarExtensions map[string]bool
	ignoredFiles      map[string]bool
	ignoredDirs       map[string]bool
}

// NewFileClassifier creates a new classifier with predefined rules
func NewFileClassifier() *FileClassifier {
	return &FileClassifier{
		imageExtensions: map[string]bool{
			".jpg":  true,
			".jpeg": true,
			".png":  true,
			".bmp":  true,
			".heic": true,
			".webp": true,
			//".gif":  true,
			//".tiff": true,
			//".tif":  true,
			//".heif": true,
		},
		videoExtensions: map[string]bool{
			".mp4": true,
			".mov": true,
			//".avi":  true,
			//".mkv":  true,
			//".wmv":  true,
			//".flv":  true,
			//".webm": true,
			//".m4v":  true,
			//".3gp":  true,
		},
		rawExtensions: map[string]bool{
			".cr2": true, // Canon
			".dng": true, // Adobe/Universal
			//".arw": true, // Sony
			//".cr3": true, // Canon
			//".nef": true, // Nikon
			//".orf": true, // Olympus
			//".rw2": true, // Panasonic
			//".pef": true, // Pentax
			//".raf": true, // Fujifilm
			//".raw": true, // Generic
		},
		sidecarExtensions: map[string]bool{
			".aae": true, // iPhone edit sidecar
			//".xmp": true, // Adobe metadata
			//".thm": true, // Thumbnail
		},
		ignoredFiles: map[string]bool{
			".DS_Store":   true,
			"Thumbs.db":   true,
			"desktop.ini": true,
			".picasa.ini": true,
			".nomedia":    true,
		},
		ignoredDirs: map[string]bool{
			".git":                      true,
			".svn":                      true,
			"node_modules":              true,
			".Trash":                    true,
			"$RECYCLE.BIN":              true,
			"System Volume Information": true,
		},
	}
}

// ClassifyName classifies a file name and reports if it should be ignored.
// This combines ignore and media checks so callers can make a single decision.
func (fc *FileClassifier) ClassifyName(name string) (mediaType string, shouldProcess bool, shouldIgnore bool) {
	if fc.ignoredFiles[name] {
		return MediaTypeUnknown, false, true
	}

	ext := strings.ToLower(filepath.Ext(name))

	switch {
	case fc.imageExtensions[ext]:
		return MediaTypeImage, true, false
	case fc.videoExtensions[ext]:
		return MediaTypeVideo, true, false
	case fc.rawExtensions[ext]:
		return MediaTypeRaw, true, false
	case fc.sidecarExtensions[ext]:
		return MediaTypeSidecar, true, false
	default:
		return MediaTypeUnknown, false, false
	}
}

// IsPrimarySource reports whether a media type represents an original/canonical file.
// Both IMAGE and RAW are primary sources — a DSLR root may contain only RAW files
// with no paired JPG, and those RAW files are the authoritative originals.
// SIDECAR files are metadata companions, never primary sources.
func (fc *FileClassifier) IsPrimarySource(mediaType string) bool {
	switch mediaType {
	case MediaTypeImage, MediaTypeRaw, MediaTypeVideo:
		return true
	default:
		return false
	}
}

// NeedsTranscoding reports whether a file requires on-the-fly conversion before
// being consumed by downstream processes (e.g. AI inference pipelines).
// RAW files cannot be fed directly to most models and must be decoded first.
func (fc *FileClassifier) NeedsTranscoding(mediaType string) bool {
	return mediaType == MediaTypeRaw
}

// ShouldIgnoreDir checks if a directory should be skipped entirely
func (fc *FileClassifier) ShouldIgnoreDir(name string) bool {
	return fc.ignoredDirs[name]
}
