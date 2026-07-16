package scanner

import (
	"path/filepath"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
)

// Capture role constants
const (
	CaptureRoleOriginal        = "ORIGINAL"
	CaptureRoleRaw             = "RAW"
	CaptureRoleLiveVideo       = "LIVE_VIDEO"
	CaptureRoleSidecar         = "SIDECAR"
	CaptureRoleEdited          = "EDITED"
	CaptureRoleEditedVideo     = "EDITED_VIDEO"
	CaptureRoleOriginalSidecar = "ORIGINAL_SIDECAR"
)

// prefixRule maps a known iPhone variant prefix (e.g. "IMG_E") to the
// canonical prefix that replaces it (e.g. "IMG_") to recover the original
// capture stem
type prefixRule struct {
	variant   string
	canonical string
}

var variantPrefixes = []prefixRule{
	{variant: "IMG_E", canonical: "IMG_"}, // Edited version of an original photo or video
	{variant: "IMG_O", canonical: "IMG_"}, // Original-state sidecar (e.g. AAE edits without a paired HEIC)
}

// DeriveCapture computes the capture stem and role from a filename, its
// lowercased extension, and its classified media type
//
// The stem is the base filename (no extension) with any variant prefix
// normalised back to the canonical prefix.  The role is determined by a
// combination of variant prefix, media type and extension
// Commonly found in iPhone images and videos, this logic is designed to group related files together
// (e.g. RAW + JPG pairs, edited + original variants) while distinguishing different capture groups
// (e.g. separate shoots or different devices) that happen to share the same filename
func DeriveCapture(filename, ext, mediaType string) CaptureInfo {
	base := strings.TrimSuffix(filename, filepath.Ext(filename)) // strip extension preserving case

	variant := ""
	for _, prefix := range variantPrefixes {
		if strings.HasPrefix(base, prefix.variant) {
			variant = prefix.variant
			start := len(prefix.variant)
			// Handle edge case where filename is just the variant prefix with no stem (e.g. "IMG_E.jpg")
			if len(base) > start {
				base = prefix.canonical + base[start:] // normalise to canonical prefix
			} else {
				base = prefix.canonical // edge case: filename is just the variant prefix
			}
			break
		}
	}

	role := deriveRole(variant, ext, mediaType)

	return CaptureInfo{Key: base, Role: role}
}

// deriveRole determines the capture role from variant prefix, extension, and media type
func deriveRole(variant, ext, mediaType string) string {
	switch {
	// Edited variants
	case variant == "IMG_E" && (ext == ".mov" || ext == ".mp4"):
		return CaptureRoleEditedVideo
	case variant == "IMG_E":
		return CaptureRoleEdited

	// Original-state sidecar
	case variant == "IMG_O":
		return CaptureRoleOriginalSidecar

	// No variant prefix — decide by media type / extension
	case mediaType == classifier.MediaTypeRaw:
		return CaptureRoleRaw
	case mediaType == classifier.MediaTypeSidecar:
		return CaptureRoleSidecar
	case mediaType == classifier.MediaTypeVideo:
		return CaptureRoleLiveVideo
	default:
		return CaptureRoleOriginal
	}
}
