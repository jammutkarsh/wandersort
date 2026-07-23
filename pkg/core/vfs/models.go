package vfs

import (
	"time"

	"github.com/jammutkarsh/wandersort/pkg/config"
)

// GroupBy names for Config.GroupBy — the configurable levels below <YEAR>/<MONTH>
const (
	GroupByLocation    = "location"
	GroupByDate        = "date"
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
	// CollapseLevels drops a device/orientation/media level that resolves to
	// the same folder name for the whole library — it would be a folder every
	// path passes through without ever distinguishing anything. Date and
	// location are never dropped. See uninformativeLevels.
	CollapseLevels bool
}

func DefaultConfig() Config {
	return Config{
		GroupBy:        []string{GroupByLocation, GroupByOrientation, GroupByMedia},
		Fallback:       "Unsorted",
		ClusterGap:     defaultClusterGap,
		NameCase:       CaseTitle,
		CollapseLevels: true,
	}
}

// GroupByNone is the --group-by sentinel for "no levels below Year/Month".
// It's not a GroupBy level (dirFor knows nothing about it) — ConfigFor is the
// only thing that interprets it, and the CLI rejects it mixed with real levels.
const GroupByNone = "none"

// ConfigFor is DefaultConfig with the user's config.yaml/flag settings applied:
// an empty GroupBy keeps the default levels, and the GroupByNone sentinel means
// a flat Year/Month with no levels below it. Every caller that turns app config
// into a vfs.Config goes through here, so the sentinel is interpreted one way
// only. It takes the whole *config.Configuration rather than loose fields so
// another vfs-relevant setting doesn't churn the signature — this is the one
// place vfs is allowed to know about the app's config package, which also means
// config can never import vfs (the CLI validates GroupBy* tokens for that
// reason).
func ConfigFor(appCfg *config.Configuration) Config {
	cfg := DefaultConfig()
	if appCfg == nil {
		return cfg
	}
	cfg.CollapseLevels = appCfg.CollapseLevels
	switch {
	case len(appCfg.GroupBy) == 1 && appCfg.GroupBy[0] == GroupByNone:
		cfg.GroupBy = nil
	case len(appCfg.GroupBy) > 0:
		cfg.GroupBy = appCfg.GroupBy
	}
	return cfg
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
	// suggestionDir is the directory the suggestion belongs to — the node a
	// reviewer renames. Recorded by dirFor when it emits the location level, so
	// the review tree never has to guess which depth that is (it moves with the
	// GroupBy order, and there may be no location level at all).
	suggestionDir string
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
