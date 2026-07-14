// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package classifier

import (
	"encoding/json"
	"fmt"
	"strconv"
)

const (
	MediaTypeImage   = "IMAGE"
	MediaTypeVideo   = "VIDEO"
	MediaTypeSidecar = "SIDECAR"
	MediaTypeRaw     = "RAW"
	MediaTypeUnknown = "UNKNOWN"
)

// CommonMetadata holds the common attributes across all supported file types
// All fields are strings; fields absent in a given file type are set to ""
//
// Fields are grouped by presence:
//   - File system:  present in every type (11/11)
//   - Dimensions:   present in image/video types (10/11, absent in AAE)
//   - Device/lens:  present in camera-produced files (7-9/11)
//   - GPS:          present in location-tagged files (7/11)
type CommonMetadata struct {
	// --- File system (11/11) ---
	ExifToolVersion     string `json:"ExifToolVersion"`
	SourceFile          string `json:"SourceFile"`
	Directory           string `json:"Directory"`
	FileName            string `json:"FileName"`
	FileSize            string `json:"FileSize"`
	FilePermissions     string `json:"FilePermissions"`
	FileType            string `json:"FileType"`
	FileTypeExtension   string `json:"FileTypeExtension"`
	MIMEType            string `json:"MIMEType"`
	FileModifyDate      string `json:"FileModifyDate"`
	FileAccessDate      string `json:"FileAccessDate"`
	FileInodeChangeDate string `json:"FileInodeChangeDate"`

	// --- Dimensions (10/11, absent in AAE) ---
	ImageWidth  string `json:"ImageWidth"`
	ImageHeight string `json:"ImageHeight"`
	ImageSize   string `json:"ImageSize"`
	Megapixels  string `json:"Megapixels"`

	// --- Orientation (9/11) ---
	Orientation string `json:"Orientation"`

	// --- Device / lens (7-8/11) ---
	Make      string `json:"Make"`
	Model     string `json:"Model"`
	LensModel string `json:"LensModel"`
	Software  string `json:"Software"`

	// --- Timestamps (7-8/11) ---
	CreateDate       string `json:"CreateDate"`
	ModifyDate       string `json:"ModifyDate"`
	DateTimeOriginal string `json:"DateTimeOriginal"`

	// --- Exposure (7/11) ---
	ISO                  string `json:"ISO"`
	Aperture             string `json:"Aperture"`
	FNumber              string `json:"FNumber"`
	FocalLength          string `json:"FocalLength"`
	ExposureTime         string `json:"ExposureTime"`
	ShutterSpeed         string `json:"ShutterSpeed"`
	ExposureMode         string `json:"ExposureMode"`
	ExposureProgram      string `json:"ExposureProgram"`
	ExposureCompensation string `json:"ExposureCompensation"`
	Flash                string `json:"Flash"`
	MeteringMode         string `json:"MeteringMode"`
	WhiteBalance         string `json:"WhiteBalance"`

	// --- GPS (7/11; absent in bmp, webp, cr2, aae) ---
	GPSLatitude    string `json:"GPSLatitude"`
	GPSLongitude   string `json:"GPSLongitude"`
	GPSAltitude    string `json:"GPSAltitude"`
	GPSAltitudeRef string `json:"GPSAltitudeRef"` // "0" = above sea level, "1" = below
	GPSPosition    string `json:"GPSPosition"`    // combined "lat, lon" string from exiftool
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
		ExifToolVersion:     get("ExifToolVersion"),
		SourceFile:          get("SourceFile"),
		Directory:           get("Directory"),
		FileName:            get("FileName"),
		FileSize:            get("FileSize"),
		FilePermissions:     get("FilePermissions"),
		FileType:            get("FileType"),
		FileTypeExtension:   get("FileTypeExtension"),
		MIMEType:            get("MIMEType"),
		FileModifyDate:      get("FileModifyDate"),
		FileAccessDate:      get("FileAccessDate"),
		FileInodeChangeDate: get("FileInodeChangeDate"),

		ImageWidth:  get("ImageWidth"),
		ImageHeight: get("ImageHeight"),
		ImageSize:   get("ImageSize"),
		Megapixels:  get("Megapixels"),

		Orientation: get("Orientation"),

		Make:      get("Make"),
		Model:     get("Model"),
		LensModel: get("LensModel"),
		Software:  get("Software"),

		CreateDate:       get("CreateDate"),
		ModifyDate:       get("ModifyDate"),
		DateTimeOriginal: get("DateTimeOriginal"),

		ISO:                  get("ISO"),
		Aperture:             get("Aperture"),
		FNumber:              get("FNumber"),
		FocalLength:          get("FocalLength"),
		ExposureTime:         get("ExposureTime"),
		ShutterSpeed:         get("ShutterSpeed"),
		ExposureMode:         get("ExposureMode"),
		ExposureProgram:      get("ExposureProgram"),
		ExposureCompensation: get("ExposureCompensation"),
		Flash:                get("Flash"),
		MeteringMode:         get("MeteringMode"),
		WhiteBalance:         get("WhiteBalance"),

		GPSLatitude:    get("GPSLatitude"),
		GPSLongitude:   get("GPSLongitude"),
		GPSAltitude:    get("GPSAltitude"),
		GPSAltitudeRef: get("GPSAltitudeRef"),
		GPSPosition:    get("GPSPosition"),
	}, nil
}
