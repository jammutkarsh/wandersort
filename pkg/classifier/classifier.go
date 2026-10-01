package classifier

import (
	"path/filepath"
	"regexp"
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

func NewFileClassifier() *FileClassifier {
	return &FileClassifier{
		imageExtensions: map[string]bool{
			".jpg":  true,
			".jpeg": true,
			".png":  true,
			".bmp":  true,
			".heic": true,
			".webp": true,
		},
		videoExtensions: map[string]bool{
			".mp4": true,
			".mov": true,
		},
		rawExtensions: map[string]bool{
			".cr2": true,
			".dng": true,
		},
		sidecarExtensions: map[string]bool{
			".aae": true, // photo edit sidecar
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

			// macOS index shards on every volume it indexes: never media, and
			// they flood the unsupported-type warnings. (.Trashes is the
			// per-volume trash; .Trash above is the home one.)
			".Spotlight-V100":         true,
			".fseventsd":              true,
			".DocumentRevisions-V100": true,
			".TemporaryItems":         true,
			".Trashes":                true,
		},
	}
}

// ClassifyName returns a file name's media type: one of the MediaType*
// constants, MediaTypeIgnored for clutter and MediaTypeUnknown for anything
// else. Only a real media type is processed.
func (fc *FileClassifier) ClassifyName(name string) string {
	base := filepath.Base(name)

	if fc.ignoredFiles[base] {
		return MediaTypeIgnored
	}

	// AppleDouble "._<name>" files: macOS writes them on exFAT/FAT/NTFS/SMB.
	// They carry the shadowed file's extension and are often byte-identical,
	// forming one huge bogus duplicate group. The name is enough to spot them.
	if strings.HasPrefix(base, "._") {
		return MediaTypeIgnored
	}

	ext := strings.ToLower(filepath.Ext(name))

	switch {
	case fc.imageExtensions[ext]:
		return MediaTypeImage
	case fc.videoExtensions[ext]:
		return MediaTypeVideo
	case fc.rawExtensions[ext]:
		return MediaTypeRaw
	case fc.sidecarExtensions[ext]:
		return MediaTypeSidecar
	default:
		return MediaTypeUnknown
	}
}

// libraryBundleSuffixes are folders another photo app owns: its thumbnails
// look like media, and moving its originals out breaks that app's library.
// Matched case-insensitively.
var libraryBundleSuffixes = []string{
	".photoslibrary",        // Apple Photos
	".photolibrary",         // iPhoto
	".migratedphotolibrary", // iPhoto library after Photos imported it
	".aplibrary",            // Aperture
	".lrdata",               // Lightroom previews and smart previews
	".lrlibrary",            // Lightroom (cloud) local library
	".cocatalogdb",          // Capture One catalog
}

func (fc *FileClassifier) ShouldIgnoreDir(name string) bool {
	if fc.ignoredDirs[name] {
		return true
	}
	lower := strings.ToLower(name)
	for _, suffix := range libraryBundleSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

var (
	genericDirs = map[string]bool{
		"dcim": true, "camera": true, "photos": true, "temp": true,
		"downloads": true, "desktop": true, "backup": true, "misc": true,
	}

	// no (?i): IsGenericDirName lowercases first, and case-folding made the
	// matcher backtrack through unicode.SimpleFold on every call
	genericDirPattern = regexp.MustCompile(`^(\.?(thumbnails|trashed.*|sync|cache))$` +
		`|^new folder(\s*\(\d+\))?$` +
		`|^(old\s+)?backup[\s_-]*\d*$` +
		`|^(dcim|temp|tmp|misc|downloads?)[\s_-]*\d*$` +
		`|^(whatsapp|telegram|signal)[\s_-]*(images?|videos?|media)$`)
)

// IsGenericDirName reports whether one folder name (a single segment) is a
// low-signal name (DCIM, Backup, temp, …).
func IsGenericDirName(name string) bool {
	seg := strings.ToLower(name)
	return genericDirs[seg] || genericDirPattern.MatchString(seg)
}
