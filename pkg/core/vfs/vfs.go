// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package vfs is phase 4 of the pipeline. It proposes a destination folder
// hierarchy for every master file in the library — regardless of which scan
// session indexed it — without touching anything on disk, persisting the
// proposal as PROPOSED rows in virtual_fs_entries. Each run replaces the
// previous proposal wholesale, so the same set of source files always yields
// the same proposal. The review flow (issue #8) approves or corrects it; a
// future Execute phase performs the actual copy/move.
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

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// geoResolver is the reverse-geocode seam; *location.Resolver satisfies it
type geoResolver interface {
	Lookup(ctx context.Context, lat, lon float64) (string, error)
}

type VFS struct {
	db       *db.DB
	resolver geoResolver
	log      logger.Logger
	cfg      Config
}

func New(database *db.DB, resolver *location.Resolver, log logger.Logger, cfg Config) *VFS {
	v := &VFS{
		db:  database,
		log: log,
		cfg: cfg,
	}
	if resolver != nil { // avoid a typed-nil interface; Run treats nil as "no geocoding"
		v.resolver = resolver
	}
	return v
}

// Run builds the virtual filesystem proposal for the whole library's master
// files; sessionID only stamps provenance on the rows it writes
func (v *VFS) Run(ctx context.Context, sessionID uuid.UUID) (int, error) {
	v.log.Info("Building virtual filesystem", "sessionId", sessionID)

	masters, err := v.loadMasters(ctx)
	if err != nil {
		return 0, err
	}
	if len(masters) == 0 {
		v.log.Info("No master files to organize", "sessionId", sessionID)
		return 0, nil
	}

	labels, err := v.loadLabels(ctx)
	if err != nil {
		return 0, err
	}

	deriveAll(masters)
	v.resolveLocations(ctx, masters, labels)
	clusterAndSuggest(masters, labels, v.cfg.ClusterGap)
	v.applyNameCase(masters)
	v.mergeSameLocationDays(masters)
	v.buildTargets(masters)
	if ctx.Err() != nil { // don't persist a half-built proposal on cancel
		return 0, ctx.Err()
	}

	count, err := v.persist(sessionID, masters)
	if err != nil {
		return count, err
	}

	v.log.Info("Virtual filesystem proposed", "sessionId", sessionID, "entries", count)
	return count, nil
}

// loadMasters reads every live master file in the library joined with its
// hashed metadata. Deliberately not session-scoped: the proposal must cover
// files indexed by earlier sessions too, or the output would depend on session
// history. The (file_dir, file_name) order makes clustering and collision
// suffixes deterministic (clusterAndSuggest sorts stably), unlike
// AUTOINCREMENT ids which vary with concurrent-worker insertion order
func (v *VFS) loadMasters(ctx context.Context) ([]masterFile, error) {
	var masters []masterFile
	if err := v.db.SQL.SelectContext(ctx, &masters, `
		SELECT fr.id, fr.file_dir, fr.file_name, fr.media_type, fr.file_extension, fr.file_modified_at,
			fm.exif_image_width, fm.exif_image_height, fm.exif_orientation,
			fm.exif_gps_latitude, fm.exif_gps_longitude,
			fm.exif_make, fm.exif_model, fm.exif_date_time_original, fm.exif_create_date,
			fm.exif_creation_date
		FROM live_files fr
		JOIN file_metadata fm ON fm.file_id = fr.id
		WHERE fm.is_master = 1
		ORDER BY fr.file_dir, fr.file_name`); err != nil {
		return nil, fmt.Errorf("query master files: %w", err)
	}
	for i := range masters {
		masters[i].absPath = filepath.Join(masters[i].FileDir, masters[i].FileName)
	}
	return masters, nil
}

func (v *VFS) loadLabels(ctx context.Context) ([]userLabel, error) {
	var labels []userLabel
	if err := v.db.SQL.SelectContext(ctx, &labels,
		`SELECT label, kind, time_start, time_end, gps_lat, gps_lon FROM user_labels`); err != nil {
		return nil, fmt.Errorf("query user labels: %w", err)
	}
	return labels, nil
}

// deriveAll fills the derived fields of every master from the metadata
// persisted during hashing — exiftool already ran once per file there, so the
// VFS phase never has to touch the files on disk again
func deriveAll(masters []masterFile) {
	for i := range masters {
		m := &masters[i]

		// CreationDate (QuickTime's composite, iOS videos only) carries its own
		// timezone offset; every other candidate here is a naive local
		// wall-clock string. Parsing CreationDate's offset for real would shift
		// a video hours away from photos taken at the same moment (the offset
		// gets applied against an assumed-UTC zero point that the naive
		// strings never had applied to them) — stripped, its wall-clock digits
		// line back up with them, which is what actually fixes a video landing
		// in the wrong day/cluster next to its photos.
		m.takenAt = firstTime(deref(m.DBDateTaken), stripOffset(deref(m.DBCreationDate)), deref(m.DBCreateDate), m.ModifiedAt)
		if m.DBWidth != nil {
			m.width = *m.DBWidth
		}
		if m.DBHeight != nil {
			m.height = *m.DBHeight
		}
		// EXIF orientations 5-8 mean the stored pixels are rotated 90°/270°;
		// swap so the orientation slot reflects how the shot is viewed
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
// result into a confirmed home/work anchor when it's within
// location.MaxDistSquared of one — the same reach Lookup itself accepts a
// match at — otherwise a home city's own suburbs each get their own folder.
// ponytail: not a separate per-user radius; revisit if a single reach doesn't
// fit both dense and sprawling metros.
// ponytail: serial loop — the resolver caches and singleflights internally;
// parallelise only if geocoding ever shows up in profiles
func (v *VFS) resolveLocations(ctx context.Context, masters []masterFile, labels []userLabel) {
	if v.resolver == nil {
		return
	}
	var anchors []userLabel
	// anchorNames maps each anchor's saved Label — the fully-qualified form
	// ResolveByName round-trips on, e.g. "Indore, Madhya Pradesh, India" — to
	// the bare city a fold should use as the folder name. The qualifiers only
	// exist to pick the right row out of the gazetteer; a folder can't hold a
	// comma, and it's the user's own home/work town, so a second disambiguation
	// pass isn't needed the way it is for an arbitrary photo's resolved city.
	anchorNames := make(map[string]string)
	for _, l := range labels {
		if (l.Kind == "ANCHOR_HOME" || l.Kind == "ANCHOR_WORK") && l.GPSLat != nil && l.GPSLon != nil {
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
		city, err := v.resolver.Lookup(ctx, m.lat, m.lon)
		if err != nil {
			v.log.Debug("No location for coordinates", "lat", m.lat, "lon", m.lon, "error", err)
			continue
		}
		m.location = city
		for _, a := range anchors {
			dLat, dLon := m.lat-*a.GPSLat, m.lon-*a.GPSLon
			if dLat*dLat+dLon*dLon <= location.MaxDistSquared {
				if v.cfg.HomeWorkDateOnly {
					// Everyday place: no location level at all — group by date
					// only. atHomeWork also keeps clusterAndSuggest from folding
					// this file back into a located cluster or hanging a
					// suggestion off it.
					m.location = ""
					m.atHomeWork = true
				} else {
					// Legacy behaviour: fold the suburb into the town's folder.
					m.location = anchorNames[a.Label]
				}
				break
			}
		}
	}
}

// applyNameCase normalises derived names (locations, suggestions) to the
// configured case style, in one place after all naming decisions are made.
// Device names, filenames, and user-confirmed labels are left alone —
// re-casing "iPhone" or a name the user typed would do more harm than good
func (v *VFS) applyNameCase(masters []masterFile) {
	for i := range masters {
		masters[i].location = caseName(masters[i].location, v.cfg.NameCase)
		if masters[i].suggestionSource != SuggestionUserLabel {
			masters[i].suggestion = caseName(masters[i].suggestion, v.cfg.NameCase)
		}
	}
}

// mergeSameLocationDays collapses runs of consecutive days that share the same
// location into one dated range folder — e.g. three Goa days
// (2024/08/02/Goa, /03/Goa, /04/Goa) become 2024/08/02_04/Goa. It only fires
// when a date level sits ABOVE a location level in the grouping (date before
// location), since that is the only shape where a per-day folder holds a
// location beneath it. Files whose location differs on an interleaving day are
// untouched, so Goa on 02-04 can merge while a Pune day at 03 keeps its own
// folder. atHomeWork (date-only) files have no location and never merge.
// The reviewer can still split a merged range in the review TUI.
func (v *VFS) mergeSameLocationDays(masters []masterFile) {
	if !v.cfg.MergeSameLocationDays {
		return
	}
	di, li := slices.Index(v.cfg.Rules, RuleDate), slices.Index(v.cfg.Rules, RuleLocation)
	if di < 0 || li < 0 || di > li {
		return // needs a date level sitting above a location level
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

	// For each key, label every day that falls inside a run of length ≥ 2.
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

// buildTargets derives the destination path for every master independently —
// each file's own derived time/location decides its directory (dirFor), full
// stop. An earlier version force-grouped files sharing a filename stem (Live
// Photo HEIC+MOV pairs, RAW+JPG, edited variants, .aae sidecars) into one
// directory, on the assumption that same-stem meant same-capture. It
// doesn't: phone/camera filename counters get reused across entirely
// unrelated shoots (old iPhone photos in particular), so two files sharing a
// stem aren't reliably the same event — that assumption was forcing
// unrelated files together. A .aae sidecar with no timestamp of its own
// falls back to file mtime via deriveAll's takenAt, same as any other file —
// in practice close enough to its paired photo's own time.
func (v *VFS) buildTargets(masters []masterFile) {
	skip := v.uninformativeLevels(masters)
	taken := map[string]bool{}
	for i := range masters {
		dir := v.dirFor(&masters[i], skip)
		name := masters[i].FileName
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		ext := filepath.Ext(name)

		// suffix only kicks in on a genuine collision — two unrelated files
		// that independently land on the exact same dir+stem+ext
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

// dirFor derives the directory segments for one master, honouring Rules
// order. skip names the levels uninformativeLevels found nothing to say with.
func (v *VFS) dirFor(m *masterFile, skip map[string]bool) string {
	if m.takenAt.IsZero() {
		// ponytail: no usable timestamp at all (should never happen — file
		// mtime is always present); park flat under the fallback dir
		return sanitizeSegment(v.cfg.Fallback)
	}

	parts := []string{
		strconv.Itoa(m.takenAt.Year()),
		// number-first ("06_June") so months sort chronologically everywhere
		// they're listed — the review tree, Finder, ls. Bare month names sort
		// alphabetically, which put December above November.
		m.takenAt.Format("01_January"),
	}
	for _, level := range v.cfg.Rules {
		if skip[level] {
			continue
		}
		seg := v.segmentFor(m, level)
		if seg == "" {
			continue // level not derivable for this file — skip the folder
		}
		parts = append(parts, sanitizeSegment(seg))
		if level == RuleLocation {
			// the folder a reviewer renames, recorded by path rather than by
			// depth so any Rules order works (and "no location level" means
			// no suggestion node at all, instead of one landing on a Device)
			m.suggestionDir = strings.Join(parts, "/")
		}
	}
	return strings.Join(parts, "/")
}

// segmentFor is the folder name one grouping level gives this file, or "" when
// the level isn't derivable for it (unknown device, no dimensions).
func (v *VFS) segmentFor(m *masterFile, level string) string {
	switch level {
	case RuleLocation:
		// ladder: resolved city → dated event segment → nothing.
		//
		// A Day level already carries the date, so the dated placeholder rung
		// is skipped there — it produced a second date beside it ("…/03/03-05/").
		//
		// There is deliberately no device or "Unsorted" rung below that: a
		// location folder named after the camera is wrong information, and it
		// duplicated the device level standing right next to it
		// ("…/Canon EOS 700D/Canon EOS 700D/"). When we don't know where a
		// photo was taken we say nothing rather than something false — the
		// level is simply absent for that file.
		switch {
		case m.location != "":
			return m.location
		case m.eventSegment != "" && !slices.Contains(v.cfg.Rules, RuleDate):
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

// collapsibleLevels are the grouping levels worth dropping when they turn out
// to carry no information. Date and location are never dropped even if the
// whole library shares one value: they're how a person recognizes a folder,
// and the review TUI's merge is the way to fold days together on purpose.
var collapsibleLevels = map[string]bool{
	RuleDevice:      true,
	RuleOrientation: true,
	RuleMedia:       true,
}

// uninformativeLevels finds the collapsible grouping levels that resolve to at
// most one distinct folder name across the whole library. Such a level adds a
// folder every path has to pass through without ever telling the user
// anything — "…/Goa/iPhone/Vertical/Photos/" when every file is a vertical
// iPhone photo. Dropping it is what makes the useful folder reachable in one
// click instead of four.
//
// Deliberately measured library-wide rather than per-branch: a level kept in
// one Day and dropped in the next would make the hierarchy a different depth
// depending on where you are, which is worse to navigate than one extra level.
func (v *VFS) uninformativeLevels(masters []masterFile) map[string]bool {
	if !v.cfg.CollapseLevels {
		return nil
	}
	seen := map[string]map[string]bool{}
	for _, level := range v.cfg.Rules {
		if collapsibleLevels[level] {
			seen[level] = map[string]bool{}
		}
	}
	for i := range masters {
		for level := range seen {
			if seg := v.segmentFor(&masters[i], level); seg != "" {
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

// persist replaces the whole previous proposal — one live proposal set for the
// library, whichever sessions wrote it. The delete runs through the same FIFO
// writer as the inserts, so a rebuild leaves no stale rows for files that are
// no longer masters.
// ponytail: revisit preserving APPROVED rows when the review/execute flow
// (issue #8) lands — today every run regenerates from scratch
func (v *VFS) persist(sessionID uuid.UUID, masters []masterFile) (int, error) {
	if !v.db.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM virtual_fs_entries`)
		return err
	}) {
		return 0, fmt.Errorf("clear previous vfs proposal: writer closed")
	}
	for i := range masters {
		m := masters[i]
		if !v.db.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO virtual_fs_entries
					(session_id, file_id, source_path, target_path, cluster_id, status, suggestion, suggestion_source, suggestion_dir)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				sessionID.String(), m.FileID, m.absPath, m.targetPath,
				nullable(m.clusterID), db.StatusProposed,
				nullable(m.suggestion), nullable(m.suggestionSource), nullable(m.suggestionDir)); err != nil {
				return fmt.Errorf("persist vfs entry for file %d: %w", m.FileID, err)
			}
			return nil
		}) {
			return i, fmt.Errorf("persist vfs entry for file %d: writer closed", m.FileID)
		}
	}
	return len(masters), nil
}

/* small parsing helpers — exiftool values arrive as strings */

// stripOffset removes a trailing timezone offset ("+05:30", "-07:00", "Z")
// from an exiftool timestamp, so it parses as the same naive wall-clock value
// every other capture-time tag here is (see the takenAt comment in
// deriveAll for why). Exiftool's date format uses colons, not hyphens, for
// the date portion, so the offset is the only '+'/'-' the string ever has.
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

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// caseName applies the configured case style to a derived name.
// Title case uppercases the first letter of every word (after a space,
// underscore, or hyphen) and lowercases the rest
func caseName(name, style string) string {
	switch style {
	case CaseLower:
		return strings.ToLower(name)
	case CaseUpper:
		return strings.ToUpper(name)
	case CaseAsIs:
		return name
	default: // CaseTitle
		var b strings.Builder
		b.Grow(len(name))
		startOfWord := true
		for _, r := range name {
			switch {
			case r == ' ' || r == '_' || r == '-':
				startOfWord = true
				b.WriteRune(r)
			case startOfWord:
				startOfWord = false
				b.WriteRune(unicode.ToUpper(r))
			default:
				b.WriteRune(unicode.ToLower(r))
			}
		}
		return b.String()
	}
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

// sanitizeSegment makes a derived value safe to use as a single path segment
func sanitizeSegment(seg string) string {
	seg = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', 0:
			return '-'
		}
		return r
	}, seg)
	seg = strings.Trim(seg, " .")
	if seg == "" {
		return "_"
	}
	return seg
}
