package classifier

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strconv"
)

const (
	MediaTypeImage   = "IMAGE"
	MediaTypeVideo   = "VIDEO"
	MediaTypeSidecar = "SIDECAR"
	MediaTypeRaw     = "RAW"
	MediaTypeUnknown = "UNKNOWN" // not a media file WanderSort reads
	MediaTypeIgnored = "IGNORED" // OS or app clutter (.DS_Store, AppleDouble files), skipped
)

// CommonMetadata holds the exiftool tags the pipeline stores, all strings ("" if
// absent). Add a field only together with the column that stores it.
type CommonMetadata struct {
	ImageWidth  string `json:"ImageWidth"`
	ImageHeight string `json:"ImageHeight"`
	Orientation string `json:"Orientation"`

	Make  string `json:"Make"`
	Model string `json:"Model"`

	CreateDate       string `json:"CreateDate"`
	DateTimeOriginal string `json:"DateTimeOriginal"`
	// CreationDate is QuickTime's composite tag (iOS videos only) — unlike
	// CreateDate, it carries its own timezone offset, e.g. "+05:30"
	CreationDate string `json:"CreationDate"`
	// MediaCreateDate is QuickTime's track-level tag; fallback for files
	// whose top-level CreateDate is missing/bogus (e.g. epoch, pre-1970)
	MediaCreateDate string `json:"MediaCreateDate"`

	GPSLatitude  string `json:"GPSLatitude"`
	GPSLongitude string `json:"GPSLongitude"`

	// IsScreenshot reports a screen capture (screenshot or recording); see
	// screenshotMarkers.
	IsScreenshot bool
}

// screenshotMarkers maps an exiftool tag to the values an OS writes on it to
// mark a screen capture. A new platform is one more entry.
var screenshotMarkers = map[string][]string{
	"Description":        {"Screenshot"},
	"UserComment":        {"Screenshot"},
	"SamsungCaptureInfo": {"Screenshot", "Screen recording"},
}

// isScreenCapture checks the raw decoded exiftool JSON against
// screenshotMarkers.
func isScreenCapture(raw map[string]any) bool {
	for key, values := range screenshotMarkers {
		v, ok := raw[key]
		if !ok || v == nil {
			continue
		}
		if slices.Contains(values, numStr(v)) {
			return true
		}
	}
	return false
}

// ftoa formats a float without a trailing exponent or ".0".
func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// numStr converts a decoded JSON value (string, number or bool) to a string:
// exiftool is inconsistent about which it emits.
func numStr(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case float64:
		return ftoa(v)
	default:
		return fmt.Sprint(v)
	}
}

// ParseMetadata parses one file's exiftool JSON into CommonMetadata. It decodes
// into a map (exiftool's fields are open-ended and loosely typed) and reads
// only the keys it needs, so one odd tag never drops the rest.
func ParseMetadata(ext string, data []byte) (CommonMetadata, error) {
	var raw map[string]any
	// a tag's bytes are the camera's: tolerate what v1 tolerated
	if err := json.Unmarshal(data, &raw,
		jsontext.AllowInvalidUTF8(true), jsontext.AllowDuplicateNames(true)); err != nil {
		return CommonMetadata{}, fmt.Errorf("parse %s: %w", ext, err)
	}

	get := func(key string) string {
		if v, ok := raw[key]; ok && v != nil {
			return numStr(v)
		}
		return ""
	}

	return CommonMetadata{
		ImageWidth:  get("ImageWidth"),
		ImageHeight: get("ImageHeight"),
		Orientation: get("Orientation"),

		Make:  get("Make"),
		Model: get("Model"),

		CreateDate:       get("CreateDate"),
		DateTimeOriginal: get("DateTimeOriginal"),
		CreationDate:     get("CreationDate"),
		MediaCreateDate:  get("MediaCreateDate"),

		GPSLatitude:  get("GPSLatitude"),
		GPSLongitude: get("GPSLongitude"),

		IsScreenshot: isScreenCapture(raw),
	}, nil
}
