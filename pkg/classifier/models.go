package classifier

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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

// itoa and ftoa are package-level helpers used by ToCommon adapters
func itoa(v int) string     { return strconv.Itoa(v) }
func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// Metadata is the common interface implemented by all file-type metadata structs
type Metadata interface {
	MediaType() string
	ToCommon() CommonMetadata
}

// ParseFromBytes decodes a JSON byte slice into the target metadata struct T
func ParseFromBytes[T Metadata](data []byte) (T, error) {
	var m T
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("unmarshal: %w", err)
	}
	return m, nil
}

// ParseMetadata parses the raw JSON bytes representing EXIF metadata for a given file extension
// and returns the unified CommonMetadata representation
func ParseMetadata(ext string, data []byte) (CommonMetadata, error) {
	var m Metadata
	var err error

	switch strings.ToLower(ext) {
	case ".jpg":
		m, err = ParseFromBytes[Jpg](data)
	case ".jpeg":
		m, err = ParseFromBytes[Jpeg](data)
	case ".heic":
		m, err = ParseFromBytes[Heic](data)
	case ".png":
		m, err = ParseFromBytes[Png](data)
	case ".bmp":
		m, err = ParseFromBytes[Bmp](data)
	case ".webp":
		m, err = ParseFromBytes[Webp](data)
	case ".cr2":
		m, err = ParseFromBytes[Cr2](data)
	case ".dng":
		m, err = ParseFromBytes[Dng](data)
	case ".mp4":
		m, err = ParseFromBytes[Mp4](data)
	case ".mov":
		m, err = ParseFromBytes[Mov](data)
	case ".aae":
		m, err = ParseFromBytes[Aae](data)
	default:
		return CommonMetadata{}, fmt.Errorf("unsupported extension: %q", ext)
	}

	if err != nil {
		return CommonMetadata{}, fmt.Errorf("parse %s: %w", ext, err)
	}
	return m.ToCommon(), nil
}
