package vfs

import "time"

// GroupBy names for Config.GroupBy — the configurable levels below <YEAR>/<MONTH>
const (
	GroupByLocation    = "location"
	GroupByDevice      = "device"
	GroupByOrientation = "orientation"
	GroupByMedia       = "media"
)

// Suggestion provenance stored on virtual_fs_entries.suggestion_source
const (
	SuggestionUserLabel    = "USER_LABEL"
	SuggestionSpillover    = "SPILLOVER"
	SuggestionAnchor       = "ANCHOR"
	SuggestionSourceFolder = "SOURCE_FOLDER"
)

// Case styles for derived folder names (locations, suggestions).
// Device names and filenames are never re-cased
const (
	CaseTitle = "title" // "goa beach" → "Goa Beach"
	CaseLower = "lower" // "Goa Beach" → "goa beach"
	CaseUpper = "upper" // "Goa Beach" → "GOA BEACH"
	CaseAsIs  = "asis"  // keep whatever the source provided
)

// defaultClusterGap is the capture-time gap that starts a new event cluster
const defaultClusterGap = 12 * time.Hour

// Config controls the shape of the proposed hierarchy
type Config struct {
	GroupBy    []string      // ordered levels below <YEAR>/<MONTH>; see GroupBy* constants
	Fallback   string        // last-resort path segment when nothing can be derived
	ClusterGap time.Duration // capture-time gap that starts a new event cluster
	NameCase   string        // case style for derived names; see Case* constants
}

func DefaultConfig() Config {
	return Config{
		GroupBy:    []string{GroupByLocation, GroupByOrientation, GroupByMedia},
		Fallback:   "Unsorted",
		ClusterGap: defaultClusterGap,
		NameCase:   CaseTitle,
	}
}

// masterFile carries one master file through the build:
// DB row → derived segments → proposed entry
type masterFile struct {
	FileID     int64  `db:"id"`
	FileDir    string `db:"file_dir"`
	FileName   string `db:"file_name"`
	MediaType  string `db:"media_type"`
	Extension  string `db:"file_extension"`
	ModifiedAt string `db:"file_modified_at"`

	// metadata persisted during hashing
	DBWidth        *int64   `db:"exif_image_width"`
	DBHeight       *int64   `db:"exif_image_height"`
	DBOrientation  *int64   `db:"exif_orientation"`
	DBLat          *float64 `db:"exif_gps_latitude"`
	DBLon          *float64 `db:"exif_gps_longitude"`
	DBMake         *string  `db:"exif_make"`
	DBModel        *string  `db:"exif_model"`
	DBDateTaken    *string  `db:"exif_date_time_original"`
	DBCreateDate   *string  `db:"exif_create_date"`
	DBCreationDate *string  `db:"exif_creation_date"`

	absPath          string
	takenAt          time.Time
	width, height    int64
	hasGPS           bool
	lat, lon         float64
	device           string
	location         string // resolved city; "" when unknown
	clusterID        string // set when the location decision came from cluster logic
	eventSegment     string // dated segment for unresolved clusters, e.g. "03-05"
	suggestion       string
	suggestionSource string
	targetPath       string
}

// userLabel is a confirmed name from a previous review, read for suggestions
// (EVENT) or to fold nearby suburbs into one anchor city (ANCHOR_HOME/WORK)
type userLabel struct {
	Label     string   `db:"label"`
	Kind      string   `db:"kind"`
	TimeStart *string  `db:"time_start"`
	TimeEnd   *string  `db:"time_end"`
	GPSLat    *float64 `db:"gps_lat"`
	GPSLon    *float64 `db:"gps_lon"`
}
