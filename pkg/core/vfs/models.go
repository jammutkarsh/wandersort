// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"time"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/location"
)

// Rules names for Config.Rules — the configurable levels below <YEAR>/<MONTH>
const (
	RuleLocation    = "location"
	RuleDate        = "date"
	RuleDevice      = "device"
	RuleOrientation = "orientation"
	RuleMedia       = "media"
)

// defaultClusterGap is the capture-time gap that starts a new event cluster
const defaultClusterGap = 12 * time.Hour

// Config controls the shape of the proposed hierarchy
type Config struct {
	Rules      []string      // ordered levels below <YEAR>/<MONTH>; see Rules* constants
	Fallback   string        // last-resort path segment when nothing can be derived
	ClusterGap time.Duration // capture-time gap that starts a new event cluster
	// Drops a device/orientation/media level with one library-wide value.
	// Date/location never drop. See uninformativeLevels.
	CollapseLevels bool
	// Groups saved-place photos by date only, no location folder. See resolveLocations.
	SavedPlacesDateOnly bool
	// Collapses consecutive same-location days into one range folder
	// (Aug/02+03+04/Goa → Aug/02_04/Goa). See mergeSameLocationDays.
	MergeSameLocationDays bool
	// Anchors are saved places resolved to GPS coordinates, built from
	// config.yaml by the caller (workflow or cli's rebuildTree). Nil when
	// no saved places are configured or the resolver isn't ready.
	Anchors []location.Anchor
	// SavedPlaces is the same places as the names the user typed, before
	// resolution. Anchors can't stand in for them in ConfigStamp: resolving
	// needs the location database, and the stamp check must work without it.
	SavedPlaces []string
	// Workers sizes the pool every per-master pass fans out over — deriveAll,
	// resolveLocations, applyNameCase and buildTargets (see forEachMaster).
	// 0 or 1 runs them inline.
	Workers int
	// Placed is the library-relative path of every file already placed, loaded
	// by Run. buildTargets never hands one of these names to a new file.
	Placed []string
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

// OrphanDir is the one flat folder every ungrouped .AAE sidecar lands in
// instead of wherever its file mtime would otherwise place it. A sidecar
// carries no EXIF and is meaningless without the photo it edits — useful only
// for re-importing into Apple Photos, never for browsing — so a sidecar
// captureDirs couldn't pair with a photo is junk to be held somewhere, not
// filed. Bypasses Rules entirely, the same way a screenshot's Year/Month
// short-circuit does. Excluded from BuildTree (nothing to review about junk)
// and therefore never renamed or merged by the reviewer.
const OrphanDir = "orphan"

// ConfigFor is DefaultConfig with the user's settings applied — the one
// place vfs imports pkg/config, so config can never import vfs back.
func ConfigFor(appCfg *config.Configuration) Config {
	cfg := DefaultConfig()
	if appCfg == nil {
		return cfg
	}
	cfg.CollapseLevels = appCfg.CollapseLevels
	cfg.SavedPlacesDateOnly = appCfg.SavedPlacesDateOnly
	cfg.MergeSameLocationDays = appCfg.MergeSameLocationDays
	cfg.Workers = appCfg.Workers
	cfg.SavedPlaces = appCfg.SavedPlaces
	switch {
	// empty Rules keeps the defaults; RuleNone is interpreted only here, so
	// every caller sees the sentinel resolved the same way
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
	FileHash   string `db:"file_hash"`
	MediaType  string `db:"media_type"`
	Extension  string `db:"file_extension"`
	ModifiedAt string `db:"file_modified_at"`

	// metadata persisted during hashing
	DBWidth           *int64   `db:"exif_image_width"`
	DBHeight          *int64   `db:"exif_image_height"`
	DBOrientation     *int64   `db:"exif_orientation"`
	DBLat             *float64 `db:"exif_gps_latitude"`
	DBLon             *float64 `db:"exif_gps_longitude"`
	DBMake            *string  `db:"exif_make"`
	DBModel           *string  `db:"exif_model"`
	DBDateTaken       *string  `db:"exif_date_time_original"`
	DBCreateDate      *string  `db:"exif_create_date"`
	DBCreationDate    *string  `db:"exif_creation_date"`
	DBMediaCreateDate *string  `db:"exif_media_create_date"`
	IsScreenshot      bool     `db:"is_screenshot"`

	absPath string
	takenAt time.Time
	// folderDate is the capture time the Year/Month folders come from: the
	// *cluster's* start, so a trip running Dec 30 → Jan 2 lands in one month
	// folder instead of being torn across two Year trees. Zero until
	// clusterAndSpill runs (PreviewPaths never clusters) — read it through
	// folderTime, never directly.
	folderDate time.Time
	// orderTime/orderHash are what buildTargets ranks a file by when names
	// collide: its own capture time and hash, or its capture group's leader's
	// (see captureDirs), so a sidecar keeps the same suffix as its photo.
	orderTime time.Time
	orderHash string
	// pairKey names the capture group this file shares one _N suffix with
	// (captureDirs, pairLiveVideos); "" = a group of one.
	pairKey       string
	width, height int64
	hasGPS        bool
	lat, lon      float64
	device        string
	location      string // resolved city; "" when unknown
	atSavedPlace  bool   // GPS at a confirmed saved-place place; suppresses the location level (SavedPlacesDateOnly)
	// keepLocationFolder overrides that suppression for this file because its
	// day holds files from somewhere else too. See unsuppressMixedSavedPlaces.
	keepLocationFolder bool
	clusterID          string // set when the location decision came from cluster logic
	eventSegment       string // dated segment for unresolved clusters, e.g. "03-05"
	dayOverride        string // date-level range label from mergeSameLocationDays, e.g. "02_04"
	targetPath         string
	// the folder the location level emitted, recorded by dirFor so the review
	// tree can hang this file's GPS off the right node without guessing a depth
	locationDir string
}

// folderTime is the instant every dated folder decision is made from — the
// cluster's start where there is one, the file's own capture time otherwise.
// One accessor, so no caller has to remember the fallback (and none of them
// can disagree about it: dirFor, locationParent, mergeSameLocationDays and
// persist all have to land the file in the same month).
func (m *masterFile) folderTime() time.Time {
	if m.folderDate.IsZero() {
		return m.takenAt
	}
	return m.folderDate
}
