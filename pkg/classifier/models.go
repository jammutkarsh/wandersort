// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package classifier

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
)

const (
	MediaTypeImage   = "IMAGE"
	MediaTypeVideo   = "VIDEO"
	MediaTypeSidecar = "SIDECAR"
	MediaTypeRaw     = "RAW"
	MediaTypeUnknown = "UNKNOWN"
)

// CommonMetadata holds the tags the pipeline stores and reads, all strings; one
// absent from a given type is "". Each earns its place: dimensions and
// orientation for the orientation folder, coordinates for the place, make and
// model for the device folder, the four capture times for the date, the
// screenshot flag for Screenshots. Anything else exiftool reports is left
// undecoded — add a field here only together with the column that stores it.
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

	// IsScreenshot reports the file was captured from a screen — a
	// screenshot or a screen recording — rather than a camera. Absent from
	// camera-taken media; see screenshotMarkers for what sets it.
	IsScreenshot bool
}

// screenshotMarkers maps an exiftool JSON tag name to the value(s) an OS/OEM
// writes on it to mark a screen capture. Checked against the raw decoded
// JSON, not CommonMetadata, so a new platform is one more table entry — no
// new struct field. macOS/iOS write "Screenshot" to both Description and
// UserComment; Samsung (One UI) writes "Screenshot" or "Screen recording"
// (video) to SamsungCaptureInfo.
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

// numStr converts a decoded JSON value (string, number, or bool) to its string
// form. This is what makes parsing tolerant: exiftool is inconsistent about
// whether a given tag is emitted as a string or a number, and reading through
// `any` accepts either instead of failing the whole decode.
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

// ParseMetadata parses raw exiftool JSON for a file and returns the unified
// CommonMetadata. It decodes into a generic map and reads only the keys it
// needs, so a type mismatch on any single tag (a string where a number was
// expected, or vice-versa) no longer drops all metadata for the file.
func ParseMetadata(ext string, data []byte) (CommonMetadata, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
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
