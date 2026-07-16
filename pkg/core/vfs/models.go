package vfs

import "time"

// Slot names for Config.Slots — the configurable levels below <YEAR>/<MONTH>
const (
	SlotLocation    = "location"
	SlotDevice      = "device"
	SlotOrientation = "orientation"
	SlotMedia       = "media"
)

// Suggestion provenance stored on virtual_fs_entries.suggestion_source
const (
	SuggestionUserLabel    = "USER_LABEL"
	SuggestionSpillover    = "SPILLOVER"
	SuggestionAnchor       = "ANCHOR"
	SuggestionSourceFolder = "SOURCE_FOLDER"
)

// defaultClusterGap is the capture-time gap that starts a new event cluster
const defaultClusterGap = 12 * time.Hour

// Config controls the shape of the proposed hierarchy
type Config struct {
	Slots      []string      // ordered slots below <YEAR>/<MONTH>; see Slot* constants
	Fallback   string        // last-resort path segment when nothing can be derived
	ClusterGap time.Duration // capture-time gap that starts a new event cluster
	Workers    int           // bounded fan-out for exiftool extraction
}

func DefaultConfig(workers int) Config {
	return Config{
		Slots:      []string{SlotLocation, SlotOrientation, SlotMedia},
		Fallback:   "Unsorted",
		ClusterGap: defaultClusterGap,
		Workers:    max(workers, 1),
	}
}

// masterFile carries one master file through the build:
// DB row → fresh EXIF → derived segments → proposed entry
type masterFile struct {
	FileID     int64  `db:"id"`
	FilePath   string `db:"file_path"`
	SourceRoot string `db:"source_root"`
	MediaType  string `db:"media_type"`
	Extension  string `db:"file_extension"`
	ModifiedAt string `db:"file_modified_at"`

	// metadata persisted during hashing — fallback when fresh extraction fails
	DBWidth      *int64   `db:"exif_image_width"`
	DBHeight     *int64   `db:"exif_image_height"`
	DBLat        *float64 `db:"exif_gps_latitude"`
	DBLon        *float64 `db:"exif_gps_longitude"`
	DBMake       *string  `db:"exif_make"`
	DBModel      *string  `db:"exif_model"`
	DBDateTaken  *string  `db:"exif_date_time_original"`
	DBCreateDate *string  `db:"exif_create_date"`

	absPath          string
	takenAt          time.Time
	width, height    int64
	hasGPS           bool
	lat, lon         float64
	device           string
	location         string // resolved city; "" when unknown
	clusterID        string // set when the location decision came from cluster logic
	eventSegment     string // dated segment for unresolved clusters, e.g. "Jun_03-05"
	suggestion       string
	suggestionSource string
	targetPath       string
}

// userLabel is a confirmed name from a previous review, read for suggestions
type userLabel struct {
	Label     string  `db:"label"`
	Kind      string  `db:"kind"`
	TimeStart *string `db:"time_start"`
	TimeEnd   *string `db:"time_end"`
}
