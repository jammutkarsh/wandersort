// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"time"

	"github.com/jammutkarsh/wandersort/pkg/config"
)

// Rules names for Config.Rules — the configurable levels below <YEAR>/<MONTH>
const (
	RuleLocation    = "location"
	RuleDate        = "date"
	RuleDevice      = "device"
	RuleOrientation = "orientation"
	RuleMedia       = "media"
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
	Rules      []string      // ordered levels below <YEAR>/<MONTH>; see Rules* constants
	Fallback   string        // last-resort path segment when nothing can be derived
	ClusterGap time.Duration // capture-time gap that starts a new event cluster
	// CollapseLevels drops a device/orientation/media level that resolves to
	// the same folder name for the whole library — it would be a folder every
	// path passes through without ever distinguishing anything. Date and
	// location are never dropped. See uninformativeLevels.
	CollapseLevels bool
	// SavedPlacesDateOnly suppresses the location level for photos taken at a
	// confirmed saved place: those everyday photos are grouped by date
	// only, not by location (see resolveLocations). When false, a saved place
	// instead folds nearby suburbs into that town's own folder.
	SavedPlacesDateOnly bool
	// MergeSameLocationDays collapses consecutive day folders that share the
	// same location into one dated range folder (Aug/02/Goa + 03/Goa + 04/Goa
	// → Aug/02_04/Goa). Only applies when a date level sits above location.
	// See mergeSameLocationDays.
	MergeSameLocationDays bool
}

func DefaultConfig() Config {
	return Config{
		Rules:                 []string{RuleDate, RuleLocation},
		Fallback:              "Unsorted",
		ClusterGap:            defaultClusterGap,
		CollapseLevels:        true,
		SavedPlacesDateOnly:   true,
		MergeSameLocationDays: true,
	}
}

// RuleNone is the config.yaml `rules` sentinel for "no levels below
// Year/Month". Not a level itself: ConfigFor is the only thing that
// interprets it.
const RuleNone = "none"

// ConfigFor is DefaultConfig with the user's settings applied: empty Rules
// keeps the defaults, RuleNone means a flat Year/Month. Every caller goes
// through here, so the sentinel is interpreted one way only.
//
// Takes the whole *config.Configuration so a new vfs-relevant setting doesn't
// churn the signature. This is the one place vfs imports pkg/config, which is
// why config can never import vfs — the CLI validates Rules* tokens instead.
func ConfigFor(appCfg *config.Configuration) Config {
	cfg := DefaultConfig()
	if appCfg == nil {
		return cfg
	}
	cfg.CollapseLevels = appCfg.CollapseLevels
	cfg.SavedPlacesDateOnly = appCfg.SavedPlacesDateOnly
	cfg.MergeSameLocationDays = appCfg.MergeSameLocationDays
	switch {
	case len(appCfg.Rules) == 1 && appCfg.Rules[0] == RuleNone:
		cfg.Rules = nil
	case len(appCfg.Rules) > 0:
		cfg.Rules = appCfg.Rules
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
	IsScreenshot   bool     `db:"is_screenshot"`

	absPath          string
	takenAt          time.Time
	width, height    int64
	hasGPS           bool
	lat, lon         float64
	device           string
	location         string // resolved city; "" when unknown
	atSavedPlace     bool   // GPS at a confirmed saved-place place; suppresses the location level (SavedPlacesDateOnly)
	clusterID        string // set when the location decision came from cluster logic
	eventSegment     string // dated segment for unresolved clusters, e.g. "03-05"
	dayOverride      string // date-level range label from mergeSameLocationDays, e.g. "02_04"
	suggestion       string
	suggestionSource string
	targetPath       string
	// suggestionDir is the directory the suggestion belongs to — the node a
	// reviewer renames. Recorded by dirFor when it emits the location level, so
	// the review tree never has to guess which depth that is (it moves with the
	// Rules order, and there may be no location level at all).
	suggestionDir string
}

// userLabel is a confirmed name from a previous review, read for suggestions
// (EVENT) or to fold nearby suburbs into one saved place.
type userLabel struct {
	Label     string   `db:"label"`
	Kind      string   `db:"kind"`
	TimeStart *string  `db:"time_start"`
	TimeEnd   *string  `db:"time_end"`
	GPSLat    *float64 `db:"gps_lat"`
	GPSLon    *float64 `db:"gps_lon"`
}
