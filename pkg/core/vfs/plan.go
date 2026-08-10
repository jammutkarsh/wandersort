// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

// Plan turns loaded master rows into their proposed destinations. It touches
// no database and no files: everything it needs is in masters and cfg.
func Plan(ctx context.Context, masters []masterFile, cfg Config, geo *location.Resolver, log logger.Logger) error {
	deriveAll(ctx, masters, cfg.Workers)
	resolveLocations(ctx, masters, cfg, geo, log)
	clusterAndSpill(masters, cfg.ClusterGap)
	applyNameCase(ctx, masters, cfg.Workers)
	unsuppressMixedSavedPlaces(masters, cfg)
	markUnknownLocations(masters, cfg)
	mergeSameLocationDays(masters, cfg)
	buildTargets(ctx, masters, cfg)
	if ctx.Err() != nil { // don't leave a half-built proposal for persist to write
		return ctx.Err()
	}
	return nil
}

// Sample is one synthetic file for PreviewPaths — the pre-derived form of a
// master, so a caller with no database and no geonames (the config wizard)
// can ask what folders a Config would produce.
type Sample struct {
	TakenAt      time.Time
	Location     string // resolved city; "" = unknown
	AtSavedPlace bool
	Device       string
	Width        int64
	Height       int64
	MediaType    string // classifier.MediaTypeVideo, else treated as a photo
	FileName     string
	DayOverride  string // pre-merged day range, e.g. "02_04"
}

// PreviewPaths uses the same dirFor the real pipeline does, so an example
// can never drift from the proposal it describes. Collapse is measured
// across the whole sample set, matching the library-wide rule below.
func PreviewPaths(cfg Config, samples []Sample) []string {
	masters := make([]masterFile, len(samples))
	for i, s := range samples {
		masters[i] = masterFile{
			FileName:     s.FileName,
			MediaType:    s.MediaType,
			takenAt:      s.TakenAt,
			location:     s.Location,
			atSavedPlace: s.AtSavedPlace,
			device:       s.Device,
			width:        s.Width,
			height:       s.Height,
			dayOverride:  s.DayOverride,
		}
	}
	unsuppressMixedSavedPlaces(masters, cfg)
	markUnknownLocations(masters, cfg)
	skip := uninformativeLevels(masters, cfg)
	paths := make([]string, len(masters))
	for i := range masters {
		paths[i] = dirFor(&masters[i], skip, cfg) + "/" + masters[i].FileName
	}
	return paths
}

// forEachMaster runs fn over every master on `workers` goroutines (<= 1 runs
// the plain loop). Only for passes that write index-disjointly — to fn's own
// master, or to slot i of a side slice the caller owns — and read nothing
// shared but immutable data, so no synchronisation is needed and the result is
// identical at any worker count.
func forEachMaster(ctx context.Context, masters []masterFile, workers int, fn func(i int, m *masterFile)) {
	if workers <= 1 {
		for i := range masters {
			if ctx.Err() != nil {
				return
			}
			fn(i, &masters[i])
		}
		return
	}

	// Hand out contiguous runs, not single indices: deriveAll costs a few
	// hundred nanoseconds per file, so one channel send per file made the pool
	// slower than the plain loop. Runs are small enough that an expensive
	// stretch (resolveLocations hitting uncached coordinates) still spreads.
	const runSize = 512
	type run struct {
		base    int // index this run's first master sits at, for callers that need it
		masters []masterFile
	}
	runs := make(chan run, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for r := range runs {
				for i := range r.masters {
					fn(r.base+i, &r.masters[i])
				}
			}
		})
	}
	base := 0
	for chunk := range slices.Chunk(masters, runSize) {
		if ctx.Err() != nil {
			break
		}
		runs <- run{base, chunk}
		base += len(chunk)
	}
	close(runs)
	wg.Wait()
}

// deriveAll fills the derived fields of every master from the metadata
// persisted during hashing — exiftool already ran once per file there, so the
// VFS phase never has to touch the files on disk again
func deriveAll(ctx context.Context, masters []masterFile, workers int) {
	forEachMaster(ctx, masters, workers, func(_ int, m *masterFile) {
		// CreationDate (iOS video) carries a timezone offset; applying it as-is
		// would shift the video away from same-moment photos, which are all
		// naive local wall-clock, so stripOffset drops the offset first.
		m.takenAt = firstTime(deref(m.DBDateTaken), stripOffset(deref(m.DBCreationDate)), deref(m.DBCreateDate), deref(m.DBMediaCreateDate), m.ModifiedAt)
		if m.DBWidth != nil {
			m.width = *m.DBWidth
		}
		if m.DBHeight != nil {
			m.height = *m.DBHeight
		}
		// orientations 5-8 store the pixels rotated 90°/270°; swap so the
		// orientation level reflects how the shot is viewed
		if o := m.DBOrientation; o != nil && *o >= 5 && *o <= 8 {
			m.width, m.height = m.height, m.width
		}

		if m.DBLat != nil && m.DBLon != nil {
			m.hasGPS, m.lat, m.lon = true, *m.DBLat, *m.DBLon
		}

		m.device = deviceName(deref(m.DBMake), deref(m.DBModel))
	})
}

// resolveLocations reverse-geocodes every GPS-tagged master, then folds the
// result into a nearby confirmed saved-place anchor so a home city's own
// suburbs don't each get their own folder.
//
// This is the only part of the build that waits on anything — the resolver's
// cache is a sync.Map over a read-only database with no connection cap, so the
// lookups genuinely overlap. The result is a pure function of the coordinates,
// so fanning out changes the order of the debug lines and nothing else.
func resolveLocations(ctx context.Context, masters []masterFile, cfg Config, geo *location.Resolver, log logger.Logger) {
	if geo == nil {
		return
	}
	forEachMaster(ctx, masters, cfg.Workers, func(_ int, m *masterFile) {
		if !m.hasGPS {
			return
		}
		city, err := geo.Lookup(ctx, m.lat, m.lon)
		if err != nil {
			log.Debug("No location for coordinates", "lat", m.lat, "lon", m.lon, "error", err)
			return
		}
		m.location = city
		for _, a := range cfg.Anchors {
			dLat, dLon := m.lat-a.Lat, m.lon-a.Lon
			if dLat*dLat+dLon*dLon <= location.MaxDistSquared {
				m.location = a.FolderName
				m.atSavedPlace = true
				break
			}
		}
	})
}

// applyNameCase title-cases the derived location and device names after every
// naming decision. Filenames are left alone.
func applyNameCase(ctx context.Context, masters []masterFile, workers int) {
	forEachMaster(ctx, masters, workers, func(_ int, m *masterFile) {
		m.location = caseName(m.location)
		m.device = caseName(m.device)
	})
}

// UnknownLocation is the location folder a file with no resolvable place gets
// when located siblings share its parent folder.
const UnknownLocation = "Unknown"

// markUnknownLocations gives unlocated files a real location name, so they
// stop sitting loose next to their located siblings and are treated like any
// other location from here on — mergeSameLocationDays included, which is what
// keeps their dates from being folded differently to everyone else's.
//
// A folder whose files are *all* unlocated gets no Unknown: the level would
// hold exactly one child saying nothing the parent didn't already.
func markUnknownLocations(masters []masterFile, cfg Config) {
	if !slices.Contains(cfg.Rules, RuleLocation) {
		return
	}
	skip := uninformativeLevels(masters, cfg)
	located := map[string]bool{}
	for i := range masters {
		if m := &masters[i]; hasLocationLevel(m) && segmentFor(m, RuleLocation, cfg) != "" {
			located[locationParent(m, skip, cfg)] = true
		}
	}
	for i := range masters {
		m := &masters[i]
		// atSavedPlace under SavedPlacesDateOnly is a deliberately suppressed
		// folder, not an unknown one
		if !hasLocationLevel(m) || m.atSavedPlace || segmentFor(m, RuleLocation, cfg) != "" {
			continue
		}
		if located[locationParent(m, skip, cfg)] {
			m.location = UnknownLocation
		}
	}
}

// unsuppressMixedSavedPlaces gives a saved-place file its location folder back
// when the day it lands in also holds files from somewhere else.
//
// SavedPlacesDateOnly drops the city folder for everyday shots, which is right
// when the whole day is everyday shots — the folder would repeat the same name
// and say nothing. It is wrong the moment that day holds anything else: the
// saved-place files sit loose in the day folder while their neighbours are
// nested one level down, so the day reads as half-sorted. A real report:
// `02/` holding a bare pile of home-town photos next to `02/Unknown/`.
//
// Runs before markUnknownLocations on purpose: the lifted city is what makes
// the GPS-less files' Unknown folder appear beside it, rather than both piles
// sitting loose together.
func unsuppressMixedSavedPlaces(masters []masterFile, cfg Config) {
	if !cfg.SavedPlacesDateOnly || !slices.Contains(cfg.Rules, RuleLocation) {
		return
	}
	skip := uninformativeLevels(masters, cfg)
	// parent folder → does anything in it come from somewhere that isn't a
	// saved place (a resolved city, or nothing resolved at all)
	mixed := map[string]bool{}
	for i := range masters {
		if m := &masters[i]; hasLocationLevel(m) && !m.atSavedPlace {
			mixed[locationParent(m, skip, cfg)] = true
		}
	}
	for i := range masters {
		m := &masters[i]
		if m.atSavedPlace && hasLocationLevel(m) && mixed[locationParent(m, skip, cfg)] {
			m.keepLocationFolder = true
		}
	}
}

// hasLocationLevel reports whether dirFor emits a location level for m at all
// — an undated file goes straight to Fallback and a screenshot to Screenshots.
func hasLocationLevel(m *masterFile) bool {
	return !m.takenAt.IsZero() && !m.IsScreenshot
}

// locationParent is the folder path m's location level sits under: everything
// dirFor emits above it. Files sharing it are siblings at that level.
//
// ponytail: computed pre-merge, so a located sibling that mergeSameLocationDays
// later lifts into a day *range* leaves the Unknown behind alone in its day.
// Order the two passes properly if that shows up in practice.
func locationParent(m *masterFile, skip map[string]bool, cfg Config) string {
	parts := monthParts(m)
	for _, level := range cfg.Rules {
		if level == RuleLocation {
			break
		}
		if skip[level] {
			continue
		}
		if seg := segmentFor(m, level, cfg); seg != "" {
			parts = append(parts, path.SanitizeSegment(seg))
		}
	}
	return strings.Join(parts, "/")
}

// monthKey is the Year/Month folder pair a file lands in — from folderTime, so
// a cluster that crosses a boundary counts as the month it started in.
type monthKey struct {
	year int
	mon  time.Month
}

// dayKey is the date folder a file lands in before any merging: its month plus
// its own day-of-month. Every file of one dayKey ends up in one folder, which
// is the invariant mergeSameLocationDays has to keep.
type dayKey struct {
	monthKey
	day int
}

// runKey is one place's presence in one month — what a merge run is computed
// over, since only same-location days fold together.
type runKey struct {
	monthKey
	loc string
}

func (m *masterFile) monthKey() monthKey {
	t := m.folderTime()
	return monthKey{t.Year(), t.Month()}
}

func (m *masterFile) dayKey() dayKey {
	return dayKey{m.monthKey(), m.takenAt.Day()}
}

// crossesFolderMonth reports whether m was shot in a different month from the
// one its cluster filed it under — a New Year's Eve trip shot on Jan 01 and
// filed under December. Its bare day-of-month is a lie inside that folder
// ("01" there reads as Dec 01, and lands on top of the real Dec 01 files), so
// it gets a month-qualified day folder and stays out of the day-merge, whose
// runs and dayKey space are both day-of-month ints within one month.
func crossesFolderMonth(m *masterFile) bool {
	t, f := m.takenAt, m.folderTime()
	return t.Year() != f.Year() || t.Month() != f.Month()
}

// mergeSameLocationDays collapses runs of consecutive same-location days into
// one dated range: 2024/08/{02,03,04}/Goa becomes 2024/08/02_04/Goa. A Pune
// day interleaved at 03 keeps its own folder; the review TUI can still split one.
func mergeSameLocationDays(masters []masterFile, cfg Config) {
	if !cfg.MergeSameLocationDays {
		return
	}
	// m.location holds the real place even for saved-place files, so no special
	// casing is needed here. Location must sit at/above Date in Rules, or the
	// range folder wouldn't contain the location folder it's meant to.
	di, li := slices.Index(cfg.Rules, RuleDate), slices.Index(cfg.Rules, RuleLocation)
	if di < 0 || (li >= 0 && di > li) {
		return
	}

	// (year, month, location) → set of day-of-month present. The month is the
	// *folder's* (folderTime), so a run is grouped by where the days land, not
	// by where they were shot. A file whose own month differs from that folder
	// month is left out entirely (see crossesFolderMonth): its day-of-month
	// isn't a day of this month.
	//
	// ponytail: so a cluster crossing a month boundary gives sibling `31` and
	// `Jan_01` folders under the start month rather than a `31_01` range.
	// Number days relative to folderTime if that turns up.
	days := map[runKey]map[int]bool{}
	for i := range masters {
		m := &masters[i]
		if m.takenAt.IsZero() || m.location == "" || crossesFolderMonth(m) {
			continue
		}
		k := runKey{m.monthKey(), m.location}
		if days[k] == nil {
			days[k] = map[int]bool{}
		}
		days[k][m.takenAt.Day()] = true
	}

	// A day lives in exactly one date folder, so every file of a day has to
	// agree on the label — one location's run cannot pull half a day into a
	// range and leave the rest behind as a sibling `02`. Disagreeing days are
	// dropped from merging and act as breaks, which can settle the runs around
	// them, so this repeats until nothing new disagrees. Each pass only ever
	// adds a broken day, so it terminates.
	broken := map[dayKey]bool{}
	var label map[runKey]map[int]string
	for {
		label = map[runKey]map[int]string{}
		for k, set := range days {
			ds := make([]int, 0, len(set))
			for d := range set {
				if !broken[dayKey{k.monthKey, d}] {
					ds = append(ds, d)
				}
			}
			sort.Ints(ds)
			// label every day inside a run of 2 or more consecutive days
			for start := 0; start < len(ds); {
				end := start
				for end+1 < len(ds) && ds[end+1] == ds[end]+1 {
					end++
				}
				if end > start {
					lo, hi := ds[start], ds[end]
					rng := fmt.Sprintf("%02d_%02d", lo, hi)
					if label[k] == nil {
						label[k] = map[int]string{}
					}
					for d := lo; d <= hi; d++ {
						label[k][d] = rng
					}
				}
				start = end + 1
			}
		}

		// one label per day, or the day breaks. A file with no location of its
		// own votes for "no range" — it would be left behind in a plain day
		// folder while its neighbours moved into one.
		seen := map[dayKey]string{}
		found := false
		for i := range masters {
			m := &masters[i]
			if m.takenAt.IsZero() || crossesFolderMonth(m) {
				continue
			}
			dk := m.dayKey()
			if broken[dk] {
				continue
			}
			lbl := label[runKey{dk.monthKey, m.location}][dk.day]
			if prev, ok := seen[dk]; ok && prev != lbl {
				broken[dk] = true
				found = true
				continue
			}
			seen[dk] = lbl
		}
		if found {
			continue // a broken day can settle the runs around it — go again
		}

		for i := range masters {
			m := &masters[i]
			if m.takenAt.IsZero() || m.location == "" || crossesFolderMonth(m) {
				continue
			}
			dk := m.dayKey()
			if broken[dk] {
				continue
			}
			m.dayOverride = label[runKey{dk.monthKey, m.location}][dk.day]
		}
		return
	}
}

// buildTargets derives every master's destination independently, except for
// a best-effort capture group (see captureDirs) that forces a sidecar/RAW+JPG
// bundle into one shared directory.
func buildTargets(ctx context.Context, masters []masterFile, cfg Config) {
	skip := uninformativeLevels(masters, cfg)
	groupDirs := captureDirs(masters, skip, cfg)

	// dirFor reads one master plus the two library-wide maps above, and its only
	// write is to that master's own locationDir, so the directories fan out.
	// The collision loop below deliberately does not: `taken` decides which of
	// two files landing on the same path keeps it and which gets the _2, and
	// that is settled by the order it reaches them.
	dirs := make([]string, len(masters))
	forEachMaster(ctx, masters, cfg.Workers, func(i int, m *masterFile) {
		// captureDirs wins when it named a shared directory for this file's group
		if dir, ok := groupDirs[i]; ok {
			dirs[i] = dir
			return
		}
		dirs[i] = dirFor(m, skip, cfg)
	})

	taken := map[string]bool{}
	for i := range masters {
		dir := dirs[i]
		name := masters[i].FileName
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		ext := filepath.Ext(name)

		// only on a genuine collision: two files landing on the same
		// dir+stem+ext
		suffix := ""
		for n := 1; ; n++ {
			if n > 1 {
				suffix = fmt.Sprintf("_%d", n)
			}
			p := dir + "/" + stem + suffix + ext
			key := strings.ToLower(p)
			if !taken[key] {
				masters[i].targetPath = p
				taken[key] = true
				break
			}
		}
	}
}

// variantPrefixes folds an iPhone filename role marker to its canonical
// form, so a companion file is recognised as the same capture as its
// original: IMG_E1783 (edited) and IMG_O1783 (sidecar) both fold to IMG_1783.
var variantPrefixes = []struct{ variant, canonical string }{
	{"IMG_E", "IMG_"},
	{"IMG_O", "IMG_"},
}

// captureAgreementWindow is how far apart two EXIF capture times may sit and
// still count as one capture. Not zero: an iPhone edit (IMG_E…) is written
// after its original and can carry a DateTimeOriginal seconds later, which
// used to break the group apart and strand the .AAE sidecar in a date folder
// while its screenshot went to Screenshots. Reused filename counters — the
// thing this check defends against — are hours or days apart, never minutes.
const captureAgreementWindow = 5 * time.Minute

// captureStem normalizes a filename to the key used to group same-capture
// files: strip the extension, then fold a known variant prefix.
func captureStem(filename string) string {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	for _, p := range variantPrefixes {
		if strings.HasPrefix(base, p.variant) {
			return p.canonical + base[len(p.variant):]
		}
	}
	return base
}

// hasExifTime reports whether m's takenAt came from a real EXIF tag rather
// than deriveAll's file-mtime fallback — exif never runs on a sidecar, so
// this is false for every .AAE regardless of what takenAt ended up holding.
func (m *masterFile) hasExifTime() bool {
	return deref(m.DBDateTaken) != "" || deref(m.DBCreationDate) != "" || deref(m.DBCreateDate) != "" || deref(m.DBMediaCreateDate) != ""
}

// captureDirs finds files that are one capture split across extensions (an
// iPhone edit/sidecar bundle, or a RAW+JPG pair) and returns the directory
// they should all share, keyed by master index.
func captureDirs(masters []masterFile, skip map[string]bool, cfg Config) map[int]string {
	type group struct{ members []int }
	groups := map[string]*group{}
	for i := range masters {
		// a Live Photo's .MOV already lands next to its .HEIC via shared
		// GPS/timestamp (buildTargets); forcing it into the group dir here
		// could push it across the Photos/Videos split instead
		if masters[i].MediaType == classifier.MediaTypeVideo {
			continue
		}
		// candidate key: same source directory + same captureStem
		key := masters[i].FileDir + "|" + captureStem(masters[i].FileName)
		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
		}
		g.members = append(g.members, i)
	}

	dirs := map[int]string{}
	for _, g := range groups {
		if len(g.members) < 2 {
			continue
		}
		// A group only forms when its EXIF-timestamped members agree — a bare
		// stem match isn't enough, since camera filename counters get reused
		// across unrelated shoots. A sidecar has no EXIF time to vote with, so
		// it rides along on whatever its siblings agree on. Time agreement
		// alone isn't enough either: a counter can be reused by a *different*
		// device on the same day (e.g. after a phone upgrade), which the
		// window wouldn't catch — so members with a known device must also
		// agree on it.
		var lo, hi time.Time
		device := ""
		deviceMismatch := false
		for _, i := range g.members {
			m := &masters[i]
			if !m.hasExifTime() {
				continue
			}
			switch t := m.takenAt; {
			case lo.IsZero():
				lo, hi = t, t
			case t.Before(lo):
				lo = t
			case t.After(hi):
				hi = t
			}
			if m.device != "" {
				switch {
				case device == "":
					device = m.device
				case device != m.device:
					deviceMismatch = true
				}
			}
		}
		if lo.IsZero() || hi.Sub(lo) > captureAgreementWindow || deviceMismatch {
			continue // can't safely anchor this group — leave members independent
		}

		// representative: a screenshot outranks everything (its whole point is
		// that Rules don't apply to it, and a sidecar of one belongs in
		// Screenshots with it), then anything over a sidecar (which carries no
		// derived data of its own), then a resolved location over none (a
		// GPS-less RAW must not drag the group into the fallback its JPG
		// sibling would avoid), then canonical filename, then insertion order
		leader, bestScore := g.members[0], -1
		for _, i := range g.members {
			score := 0
			if masters[i].IsScreenshot {
				score += 8
			}
			if masters[i].MediaType != classifier.MediaTypeSidecar {
				score += 4
			}
			if masters[i].location != "" {
				score += 2
			}
			name := masters[i].FileName
			if captureStem(name) == strings.TrimSuffix(name, filepath.Ext(name)) {
				score++
			}
			if score > bestScore {
				leader, bestScore = i, score
			}
		}
		dir := dirFor(&masters[leader], skip, cfg)
		for _, i := range g.members {
			dirs[i] = dir
			// buildTargets skips dirFor for a group member, so the leader's
			// locationDir has to come along with the directory — without it
			// every grouped file wrote a NULL location_dir and the review
			// tree had no GPS to re-query for that folder's renames
			masters[i].locationDir = masters[leader].locationDir
			// …and so does the time the directory was derived from, or the
			// row's taken_at disagrees with its own path: a sidecar has no
			// EXIF time of its own (it rides along on the group, see the
			// window check above) and its file mtime can sit months away, so
			// it surfaced in a review time slice whose tree showed one lone
			// folder from a different year — a reported bug.
			//
			// ponytail: nothing to copy when the leader itself is undated, so
			// a dated member of an undated group still lands in its own slice
			// showing the Fallback folder. Give masterFile an explicit folder
			// time if that ever turns up.
			masters[i].folderDate = masters[leader].folderTime()
		}
	}
	return dirs
}

// monthParts is the Year and Month folder pair, from folderTime — so every
// file of a boundary-crossing cluster gets its cluster's month, and the two
// places that need this pair (dirFor and locationParent) can never disagree
// about which folder a file is in.
func monthParts(m *masterFile) []string {
	t := m.folderTime()
	return []string{
		strconv.Itoa(t.Year()),
		// number-first so months sort chronologically in the review tree,
		// Finder and ls; bare names put December above November
		t.Format("01_January"),
	}
}

// dirFor derives the directory segments for one master, honouring Rules
// order. skip names the levels uninformativeLevels found nothing to say with.
func dirFor(m *masterFile, skip map[string]bool, cfg Config) string {
	if m.takenAt.IsZero() {
		return path.SanitizeSegment(cfg.Fallback)
	}

	parts := monthParts(m)

	// A screenshot has no location/device/orientation worth a folder of its
	// own — group every screenshot in the month together instead of letting
	// the configured Rules fragment them.
	if m.IsScreenshot {
		return strings.Join(append(parts, "Screenshots"), "/")
	}

	for _, level := range cfg.Rules {
		if skip[level] {
			continue
		}
		seg := segmentFor(m, level, cfg)
		if seg == "" {
			continue // level not derivable for this file — skip the folder
		}
		parts = append(parts, path.SanitizeSegment(seg))
		if level == RuleLocation {
			// recorded by path, not depth, so any Rules order works — this is
			// where the review tree hangs the file's GPS for rename lookups
			m.locationDir = strings.Join(parts, "/")
		}
	}
	return strings.Join(parts, "/")
}

// segmentFor is the folder name one grouping level gives this file, or "" when
// the level isn't derivable for it (unknown device, no dimensions).
func segmentFor(m *masterFile, level string, cfg Config) string {
	switch level {
	case RuleLocation:
		// SavedPlacesDateOnly: an everyday place gets no location folder, just
		// the (possibly merged) date range — m.location itself stays real,
		// it's only the folder that's suppressed. Unless the day holds files
		// from elsewhere too; see unsuppressMixedSavedPlaces.
		if m.atSavedPlace && cfg.SavedPlacesDateOnly && !m.keepLocationFolder {
			return ""
		}
		// ladder: resolved city → dated event segment → nothing. No device or
		// "Unsorted" rung: an unknown location says nothing rather than
		// something false ("…/Canon EOS 700D/Canon EOS 700D/").
		switch {
		case m.location != "":
			return m.location
		// a Day level already carries the date; a second one beside it reads
		// as "…/03/03-05/"
		case m.eventSegment != "" && !slices.Contains(cfg.Rules, RuleDate):
			return m.eventSegment
		default:
			return ""
		}
	case RuleDate:
		if m.dayOverride != "" {
			return m.dayOverride // merged consecutive same-location day range
		}
		if crossesFolderMonth(m) {
			// the cluster filed this file under another month; a bare "02"
			// would read as that month's day and collide with it
			return m.takenAt.Format("Jan_02")
		}
		return m.takenAt.Format("02")
	case RuleDevice:
		return m.device
	case RuleOrientation:
		if m.width == 0 || m.height == 0 {
			return ""
		}
		if m.height > m.width {
			return "Vertical"
		}
		return "Horizontal"
	case RuleMedia:
		if m.MediaType == classifier.MediaTypeVideo {
			return "Videos"
		}
		return "Photos"
	}
	return ""
}

// collapsibleLevels are worth dropping when they carry no information. Date
// and location never collapse — they're how a person recognizes a folder;
// [m] in the review TUI is the deliberate way to fold days together.
var collapsibleLevels = map[string]bool{
	RuleDevice:      true,
	RuleOrientation: true,
	RuleMedia:       true,
}

// uninformativeLevels finds collapsible levels resolving to at most one folder
// name library-wide — "…/Goa/iPhone/Vertical/Photos/" is four folders deep to
// reach one when every file is a vertical iPhone photo.
func uninformativeLevels(masters []masterFile, cfg Config) map[string]bool {
	if !cfg.CollapseLevels {
		return nil
	}
	// measured library-wide, not per-branch: a level kept under one Day and
	// dropped under the next would give the tree a different depth depending
	// on where you stand
	seen := map[string]map[string]bool{}
	for _, level := range cfg.Rules {
		if collapsibleLevels[level] {
			seen[level] = map[string]bool{}
		}
	}
	for i := range masters {
		for level := range seen {
			if seg := segmentFor(&masters[i], level, cfg); seg != "" {
				seen[level][seg] = true
			}
		}
	}
	skip := map[string]bool{}
	for level, values := range seen {
		if len(values) <= 1 {
			skip[level] = true
		}
	}
	return skip
}

/* small parsing helpers — exiftool values arrive as strings */

// stripOffset removes a trailing timezone offset ("+05:30", "Z") so the value
// parses as the naive wall-clock every other capture-time tag is (see deriveAll).
// Exiftool dates use colons, so the offset is the only '+'/'-' in the string.
func stripOffset(s string) string {
	s = strings.TrimSuffix(s, "Z")
	if i := strings.LastIndexAny(s, "+-"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// looseTimeLayouts is tried in order, so the plain exiftool shape leads: it is
// what almost every DateTimeOriginal parses as, and every layout ahead of it is
// a parse attempt that fails for the common case.
var looseTimeLayouts = []string{
	"2006:01:02 15:04:05",
	"2006:01:02 15:04:05.999999999-07:00",
	"2006:01:02 15:04:05-07:00",
	"2006:01:02 15:04:05.999999999",
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func parseTimeLoose(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range looseTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			// Some devices write a bogus CreateDate (e.g. a QuickTime
			// epoch/offset bug landing pre-1970) that still parses cleanly;
			// reject it so firstTime falls through to the next candidate
			// (MediaCreateDate) instead of taking a wrong-but-valid date.
			if t.Year() < 1970 {
				return time.Time{}, false
			}
			return t, true
		}
	}
	return time.Time{}, false
}

// firstTime returns the first candidate that parses as a timestamp
func firstTime(candidates ...string) time.Time {
	for _, c := range candidates {
		if t, ok := parseTimeLoose(c); ok {
			return t
		}
	}
	return time.Time{}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// caseWhitelist are words whose casing is already correct — title-casing
// "iPhone" word-by-word would give "Iphone". Matched case-insensitively;
// extend as more turn up in any derived name.
var caseWhitelist = map[string]string{
	"iphone": "iPhone",
}

// isWordDelim reports whether r separates words in a derived name.
func isWordDelim(r rune) bool {
	return r == ' ' || r == '_' || r == '-'
}

// caseName title-cases a derived name, preserving words in caseWhitelist.
func caseName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	start := 0
	for i, r := range name {
		if !isWordDelim(r) {
			continue
		}
		b.WriteString(titleWord(name[start:i]))
		b.WriteRune(r)
		start = i + 1
	}
	b.WriteString(titleWord(name[start:]))
	return b.String()
}

// titleWord title-cases a single word unless it matches caseWhitelist.
func titleWord(w string) string {
	if exact, ok := caseWhitelist[strings.ToLower(w)]; ok {
		return exact
	}
	if w == "" {
		return ""
	}
	runes := []rune(w)
	runes[0] = unicode.ToUpper(runes[0])
	for i := 1; i < len(runes); i++ {
		runes[i] = unicode.ToLower(runes[i])
	}
	return string(runes)
}

// deviceName joins Make and Model, avoiding duplication when the model
// already contains the make (e.g. "Canon" + "Canon EOS R5")
func deviceName(mk, model string) string {
	switch {
	case model == "":
		return mk
	case mk == "" || strings.Contains(strings.ToLower(model), strings.ToLower(mk)):
		return model
	default:
		return mk + " " + model
	}
}
