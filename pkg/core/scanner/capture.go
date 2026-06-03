package scanner

import (
	"path/filepath"
	"strings"
)

// Capture role constants.
const (
	CaptureRoleOriginal        = "ORIGINAL"
	CaptureRoleRaw             = "RAW"
	CaptureRoleLiveVideo       = "LIVE_VIDEO"
	CaptureRoleSidecar         = "SIDECAR"
	CaptureRoleEdited          = "EDITED"
	CaptureRoleEditedVideo     = "EDITED_VIDEO"
	CaptureRoleOriginalSidecar = "ORIGINAL_SIDECAR"
)

// variantPrefix maps known iPhone variant prefixes to a normalisation rule.
// The key is the prefix (e.g. "IMG_E"), the value is the canonical prefix
// that replaces it (e.g. "IMG_") to recover the original capture stem.
var variantPrefixes = []CaptureInfo{
	{variant: "IMG_E", captureKey: "IMG_"}, // Edited version of an original photo or video
	{variant: "IMG_O", captureKey: "IMG_"}, // Original-state sidecar (e.g. AAE edits without a paired HEIC)
}

// deriveCapture computes the capture stem and role from a filename, its
// lowercased extension, and its classified media type.
//
// The stem is the base filename (no extension) with any variant prefix
// normalised back to the canonical prefix.  The role is determined by a
// combination of variant prefix, media type and extension.
// Commonly found in iPhone images and videos, this logic is designed to group related files together
// (e.g. RAW + JPG pairs, edited + original variants) while distinguishing different capture groups
// (e.g. separate shoots or different devices) that happen to share the same filename.
func deriveCapture(filename, ext, mediaType string) CaptureInfo {
	base := strings.TrimSuffix(filename, filepath.Ext(filename)) // strip extension preserving case

	variant := ""
	for _, vp := range variantPrefixes {
		if strings.HasPrefix(base, vp.variant) {
			variant = vp.variant
			start := len(vp.variant)
			// Handle edge case where filename is just the variant prefix with no stem (e.g. "IMG_E.jpg")
			if len(base) > start {
				base = vp.captureKey + base[start:] // normalise to canonical prefix
			} else {
				base = vp.captureKey // edge case: filename is just the variant prefix
			}
			break
		}
	}

	role := deriveRole(variant, ext, mediaType)

	return CaptureInfo{captureKey: base, variant: role}
}

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
	case mediaType == "RAW":
		return CaptureRoleRaw
	case mediaType == "SIDECAR":
		return CaptureRoleSidecar
	case mediaType == "VIDEO":
		return CaptureRoleLiveVideo
	default:
		return CaptureRoleOriginal
	}
}
