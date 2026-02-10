package scanner

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
}

// NewFileClassifier creates a new classifier with predefined rules
func NewFileClassifier() *FileClassifier {
	return &FileClassifier{
		imageExtensions: map[string]bool{
			".jpg":  true,
			".jpeg": true,
			".png":  true,
			".gif":  true,
			".bmp":  true,
			".tiff": true,
			".tif":  true,
			".heic": true,
			".heif": true,
			".webp": true,
		},
		videoExtensions: map[string]bool{
			".mp4":  true,
			".mov":  true,
			".avi":  true,
			".mkv":  true,
			".wmv":  true,
			".flv":  true,
			".webm": true,
			".m4v":  true,
			".3gp":  true,
		},
		rawExtensions: map[string]bool{
			".arw": true, // Sony
			".cr2": true, // Canon
			".cr3": true, // Canon
			".nef": true, // Nikon
			".dng": true, // Adobe/Universal
			".orf": true, // Olympus
			".rw2": true, // Panasonic
			".pef": true, // Pentax
			".raf": true, // Fujifilm
			".raw": true, // Generic
		},
		sidecarExtensions: map[string]bool{
			".aae": true, // iPhone edit sidecar
			".xmp": true, // Adobe metadata
			".thm": true, // Thumbnail
		},
	}
}

// Classify determines the media type and whether to process the file
func (fc *FileClassifier) Classify(filePath string) (mediaType string, shouldProcess bool) {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch {
	case fc.imageExtensions[ext]:
		return MediaTypeImage, true
	case fc.videoExtensions[ext]:
		return MediaTypeVideo, true
	case fc.rawExtensions[ext]:
		return MediaTypeRaw, true
	case fc.sidecarExtensions[ext]:
		return MediaTypeSidecar, true
	default:
		return MediaTypeUnknown, false
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

// ShouldIgnore checks for system files that should be skipped
func (fc *FileClassifier) ShouldIgnore(name string) bool {
	ignoredFiles := map[string]bool{
		".DS_Store":   true,
		"Thumbs.db":   true,
		"desktop.ini": true,
		".picasa.ini": true,
		".nomedia":    true,
	}

	return ignoredFiles[name]
}

// ShouldIgnoreDir checks if a directory should be skipped entirely
func (fc *FileClassifier) ShouldIgnoreDir(name string) bool {
	ignoredDirs := map[string]bool{
		".git":                      true,
		".svn":                      true,
		"node_modules":              true,
		".Trash":                    true,
		"$RECYCLE.BIN":              true,
		"System Volume Information": true,
	}

	return ignoredDirs[name]
}
