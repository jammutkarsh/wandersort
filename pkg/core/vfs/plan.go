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
	"time"
	"unicode"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Plan turns loaded master rows into their proposed destinations. It touches
// no database and no files: everything it needs is in masters, labels and cfg.
func Plan(ctx context.Context, masters []masterFile, labels []userLabel, cfg Config, geo *location.Resolver, log logger.Logger) error {
	deriveAll(masters)
	resolveLocations(ctx, masters, labels, geo, log)
	clusterAndSuggest(masters, labels, cfg.ClusterGap)
	applyNameCase(masters)
	mergeSameLocationDays(masters, cfg)
	buildTargets(masters, cfg)
	if ctx.Err() != nil { // don't leave a half-built proposal for persist to write
		return ctx.Err()
	}
	return nil
}

// Sample is one synthetic file for PreviewPaths — the pre-derived form of a
// master, so a caller with no database and no gazetteer (the config wizard)
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

// PreviewPaths is the folder path each sample would land on under cfg — the
// same dirFor the scan uses, so an example can never drift from the proposal
// it is describing. Collapse is measured across the whole sample set, exactly
// as it is library-wide in the real pipeline.
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
	skip := uninformativeLevels(masters, cfg)
	paths := make([]string, len(masters))
	for i := range masters {
		paths[i] = dirFor(&masters[i], skip, cfg) + "/" + masters[i].FileName
	}
	return paths
}

// deriveAll fills the derived fields of every master from the metadata
// persisted during hashing — exiftool already ran once per file there, so the
// VFS phase never has to touch the files on disk again
func deriveAll(masters []masterFile) {
	for i := range masters {
		m := &masters[i]

		// CreationDate (iOS videos) is the only candidate carrying a timezone
		// offset; the rest are naive local wall-clock. Applying the real offset
		// would shift a video hours away from photos of the same moment, so
		// stripOffset lines its digits back up with theirs.
		m.takenAt = firstTime(deref(m.DBDateTaken), stripOffset(deref(m.DBCreationDate)), deref(m.DBCreateDate), m.ModifiedAt)
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
	}
}

// resolveLocations reverse-geocodes every GPS-tagged master, then folds the
// result into a nearby confirmed saved-place anchor so a home city's own
// suburbs don't each get their own folder.
func resolveLocations(ctx context.Context, masters []masterFile, labels []userLabel, geo *location.Resolver, log logger.Logger) {
	if geo == nil {
		return
	}
	var anchors []userLabel
	// anchors are saved fully qualified ("Indore, Madhya Pradesh, India") so
	// ResolveByName can round-trip them; a folder gets the bare city
	anchorNames := make(map[string]string)
	for _, l := range labels {
		if l.Kind == config.SavedPlace && l.GPSLat != nil && l.GPSLon != nil {
			anchors = append(anchors, l)
			if _, ok := anchorNames[l.Label]; !ok {
				city, _, _ := strings.Cut(l.Label, ",")
				anchorNames[l.Label] = city
			}
		}
	}
	for i := range masters {
		if ctx.Err() != nil {
			return
		}
		m := &masters[i]
		if !m.hasGPS {
			continue
		}
		city, err := geo.Lookup(ctx, m.lat, m.lon)
		if err != nil {
			log.Debug("No location for coordinates", "lat", m.lat, "lon", m.lon, "error", err)
			continue
		}
		// always the real place, saved-place included — segmentFor is what
		// suppresses the folder later, so clustering and the day-range merge
		// keep working off real data without knowing about that setting
		m.location = city
		for _, a := range anchors {
			dLat, dLon := m.lat-*a.GPSLat, m.lon-*a.GPSLon
			if dLat*dLat+dLon*dLon <= location.MaxDistSquared {
				m.location = anchorNames[a.Label] // fold suburb into the confirmed saved-place town
				m.atSavedPlace = true
				break
			}
		}
	}
}

// applyNameCase title-cases derived location, suggestion, and device names
// after every naming decision. Filenames and user-confirmed labels are left alone.
func applyNameCase(masters []masterFile) {
	for i := range masters {
		masters[i].location = caseName(masters[i].location)
		if masters[i].suggestionSource != SuggestionUserLabel {
			masters[i].suggestion = caseName(masters[i].suggestion)
		}
		masters[i].device = caseName(masters[i].device)
	}
}

// mergeSameLocationDays collapses runs of consecutive same-location days into
// one dated range: 2024/08/{02,03,04}/Goa becomes 2024/08/02_04/Goa. A Pune day
// interleaved at 03 keeps its own folder, and the reviewer can still split a
// range in the review TUI.
func mergeSameLocationDays(masters []masterFile, cfg Config) {
	if !cfg.MergeSameLocationDays {
		return
	}
	// keys on m.location, the real place even for saved-place files (see
	// resolveLocations), so saved-place days merge like any trip's with no
	// special-casing here. Date must exist to hold the range label; when
	// Location is a configured Rule it must sit at or above Date, or the
	// range folder wouldn't contain the location folder it's meant to.
	di, li := slices.Index(cfg.Rules, RuleDate), slices.Index(cfg.Rules, RuleLocation)
	if di < 0 || (li >= 0 && di > li) {
		return
	}

	type key struct {
		year int
		mon  time.Month
		loc  string
	}
	// (year, month, location) → set of day-of-month present
	days := map[key]map[int]bool{}
	for i := range masters {
		m := &masters[i]
		if m.takenAt.IsZero() || m.location == "" {
			continue
		}
		k := key{m.takenAt.Year(), m.takenAt.Month(), m.location}
		if days[k] == nil {
			days[k] = map[int]bool{}
		}
		days[k][m.takenAt.Day()] = true
	}

	// label every day inside a run of 2 or more consecutive days
	label := map[key]map[int]string{}
	for k, set := range days {
		ds := make([]int, 0, len(set))
		for d := range set {
			ds = append(ds, d)
		}
		sort.Ints(ds)
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

	for i := range masters {
		m := &masters[i]
		if m.takenAt.IsZero() || m.location == "" {
			continue
		}
		k := key{m.takenAt.Year(), m.takenAt.Month(), m.location}
		if rng, ok := label[k][m.takenAt.Day()]; ok {
			m.dayOverride = rng
		}
	}
}

// buildTargets derives every master's destination independently, except for
// a best-effort capture group (see captureDirs) that forces a sidecar/RAW+JPG
// bundle into one shared directory.
func buildTargets(masters []masterFile, cfg Config) {
	skip := uninformativeLevels(masters, cfg)
	groupDirs := captureDirs(masters, skip, cfg)
	taken := map[string]bool{}
	for i := range masters {
		// captureDirs wins when it named a shared directory for this file's group
		dir, ok := groupDirs[i]
		if !ok {
			dir = dirFor(&masters[i], skip, cfg)
		}
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
			if !taken[strings.ToLower(p)] {
				masters[i].targetPath = p
				taken[strings.ToLower(p)] = true
				break
			}
		}
	}
}

// variantPrefixes maps an iPhone filename role marker to its canonical form,
// so a companion file is recognised as the same capture as its original:
// IMG_E1783 (edited copy) and IMG_O1783 (original-state sidecar with no
// paired image) both fold to IMG_1783.
var variantPrefixes = []struct{ variant, canonical string }{
	{"IMG_E", "IMG_"},
	{"IMG_O", "IMG_"},
}

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
	return deref(m.DBDateTaken) != "" || deref(m.DBCreationDate) != "" || deref(m.DBCreateDate) != ""
}

// captureDirs finds files that are one capture split across extensions — an
// iPhone edit/sidecar bundle (IMG_1783.AAE + IMG_1783.HEIC + IMG_E1783.HEIC)
// or a RAW+JPG pair — and returns the directory they should all share, keyed
// by master index.
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
		// a group only forms when every real-EXIF-timestamped member agrees to
		// the second — a bare stem match isn't enough since camera filename
		// counters get reused across unrelated shoots. A sidecar has no EXIF
		// timestamp to agree or disagree with, so it doesn't vote; it rides
		// along on whatever its timestamped siblings agree on.
		var agreed time.Time
		conflict := false
		for _, i := range g.members {
			m := &masters[i]
			if !m.hasExifTime() {
				continue
			}
			t := m.takenAt.Truncate(time.Second)
			switch {
			case agreed.IsZero():
				agreed = t
			case !t.Equal(agreed):
				conflict = true
			}
		}
		if conflict || agreed.IsZero() {
			continue // can't safely anchor this group — leave members independent
		}

		// representative: a resolved location beats none (a RAW file missing
		// GPS its JPG sibling has must not drag the group into the
		// location-less fallback), then the canonical (non-variant) filename,
		// then insertion order
		leader, bestScore := g.members[0], -1
		for _, i := range g.members {
			score := 0
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
		}
	}
	return dirs
}

// dirFor derives the directory segments for one master, honouring Rules
// order. skip names the levels uninformativeLevels found nothing to say with.
func dirFor(m *masterFile, skip map[string]bool, cfg Config) string {
	if m.takenAt.IsZero() {
		return SanitizeSegment(cfg.Fallback)
	}

	parts := []string{
		strconv.Itoa(m.takenAt.Year()),
		// number-first so months sort chronologically in the review tree,
		// Finder and ls; bare names put December above November
		m.takenAt.Format("01_January"),
	}

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
		parts = append(parts, SanitizeSegment(seg))
		if level == RuleLocation {
			// the folder a reviewer renames, recorded by path not depth so any
			// Rules order works — no location level means no suggestion node,
			// rather than one landing on a Device folder
			m.suggestionDir = strings.Join(parts, "/")
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
		// it's only the folder that's suppressed.
		if m.atSavedPlace && cfg.SavedPlacesDateOnly {
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

// collapsibleLevels are the levels worth dropping when they carry no
// information. Date and location never collapse even on one library-wide
// value: they are how a person recognizes a folder, and [m] in the review TUI
// is the deliberate way to fold days together.
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

var looseTimeLayouts = []string{
	"2006:01:02 15:04:05.999999999-07:00",
	"2006:01:02 15:04:05-07:00",
	"2006:01:02 15:04:05.999999999",
	"2006:01:02 15:04:05",
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
// "iPhone" word-by-word would give "Iphone", losing the recognizable name.
// Matched case-insensitively; extend as more turn up in any derived name
// (device, location, suggestion).
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

// SanitizeSegment makes a derived value safe to use as a single path segment.
func SanitizeSegment(seg string) string {
	// spaces and commas are routine in a geocoded place name ("Seoni,
	// Himachal Pradesh") or title-cased device name ("Samsung Galaxy S23");
	// the comma is fine anywhere a person is *choosing* a name (search
	// results, the rename dropdown), just not in the name once picked
	seg = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', 0, ' ', ',', '\t', '\n':
			return '-'
		}
		return r
	}, seg)
	for strings.Contains(seg, "--") {
		seg = strings.ReplaceAll(seg, "--", "-")
	}
	seg = strings.Trim(seg, " ._-")
	if seg == "" {
		return "-"
	}
	return seg
}
