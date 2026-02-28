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
var variantPrefixes = []struct {
	variant   string
	canonical string
}{
	{"IMG_E", "IMG_"},
	{"IMG_O", "IMG_"},
}

// CaptureInfo holds the derived capture-group fields for a single file.
type CaptureInfo struct {
	Stem string // e.g. "IMG_3162", "_MG_1721"
	Role string // one of the CaptureRole* constants
}

// DeriveCapture computes the capture stem and role from a filename, its
// lowercased extension, and its classified media type.
//
// The stem is the base filename (no extension) with any variant prefix
// normalised back to the canonical prefix.  The role is determined by a
// combination of variant prefix, media type and extension.
func DeriveCapture(filename, ext, mediaType string) CaptureInfo {
	base := strings.TrimSuffix(filename, filepath.Ext(filename)) // strip extension preserving case

	variant := ""
	for _, vp := range variantPrefixes {
		if strings.HasPrefix(base, vp.variant) {
			variant = vp.variant
			base = vp.canonical + base[len(vp.variant):] // normalise
			break
		}
	}

	role := deriveRole(variant, ext, mediaType)

	return CaptureInfo{Stem: base, Role: role}
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
