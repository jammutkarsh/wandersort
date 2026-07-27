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

func New(db *db.DB, resolver *location.Resolver, log logger.Logger, cfg Config) *VFS {
	v := &VFS{
		db:  db,
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

// loadMasters reads every live master in the library with its hashed metadata.
// Not session-scoped: the proposal must cover earlier sessions' files too, or
// the output would depend on scan history. Ordered by (file_dir, file_name),
// not id, so clustering and collision suffixes don't vary with worker order.
func (v *VFS) loadMasters(ctx context.Context) ([]masterFile, error) {
	var masters []masterFile
	if err := v.db.SQL.SelectContext(ctx, &masters, `
		SELECT fr.id, fr.file_dir, fr.file_name, fr.media_type, fr.file_extension, fr.file_modified_at,
			fm.exif_image_width, fm.exif_image_height, fm.exif_orientation,
			fm.exif_gps_latitude, fm.exif_gps_longitude,
			fm.exif_make, fm.exif_model, fm.exif_date_time_original, fm.exif_create_date,
			fm.exif_creation_date, fm.is_screenshot
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
// result into a confirmed home/work anchor within location.MaxDistSquared —
// otherwise a home city's own suburbs each get their own folder. This always
// computes the real place, home/work included: whether a home/work file's
// location folder actually renders is a HomeWorkDateOnly decision made later,
// in segmentFor. Deriving first and suppressing at render time (rather than
// blanking m.location here) keeps every later phase — clustering, the day-range
// merge — working off real data instead of each having to know about
// HomeWorkDateOnly on its own behalf.
func (v *VFS) resolveLocations(ctx context.Context, masters []masterFile, labels []userLabel) {
	if v.resolver == nil {
		return
	}
	var anchors []userLabel
	// anchors are saved fully qualified ("Indore, Madhya Pradesh, India") so
	// ResolveByName can round-trip them; a folder gets the bare city
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
				m.location = anchorNames[a.Label] // fold suburb into the confirmed home/work town
				m.atHomeWork = true
				break
			}
		}
	}
}

// applyNameCase normalises derived names once, after every naming decision.
// Filenames and user-confirmed labels are left alone — re-casing a name the
// user typed would do more harm than good. Device names always go through
// caseDevice regardless of v.cfg.NameCase: a device folder is a vendor
// string ("samsung", "SAMSUNG Galaxy S23"), not prose, so there's no reading
// under which "as typed by whichever firmware wrote the EXIF tag" is a
// choice worth exposing — Title-With-Hyphen is applied unconditionally.
func (v *VFS) applyNameCase(masters []masterFile) {
	for i := range masters {
		masters[i].location = caseName(masters[i].location, v.cfg.NameCase)
		if masters[i].suggestionSource != SuggestionUserLabel {
			masters[i].suggestion = caseName(masters[i].suggestion, v.cfg.NameCase)
		}
		masters[i].device = caseDevice(masters[i].device)
	}
}

// mergeSameLocationDays collapses runs of consecutive same-location days into
// one dated range: 2024/08/{02,03,04}/Goa becomes 2024/08/02_04/Goa. A Pune day
// interleaved at 03 keeps its own folder, and the reviewer can still split a
// range in the review TUI.
//
// This keys on m.location, which resolveLocations sets to the real place for
// home/work files too (HomeWorkDateOnly only hides the *folder*, in
// segmentFor — see its comment). So home/work days merge exactly like a trip's
// do, with no special-casing needed here.
func (v *VFS) mergeSameLocationDays(masters []masterFile) {
	if !v.cfg.MergeSameLocationDays {
		return
	}
	// Date must exist to hold the range label. Location doesn't have to be a
	// configured Rule — files can still share a real (if unrendered) place —
	// but when it is, Date has to sit at or above it, or the range folder
	// wouldn't contain the location folder it's meant to.
	di, li := slices.Index(v.cfg.Rules, RuleDate), slices.Index(v.cfg.Rules, RuleLocation)
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

// buildTargets derives every master's destination independently — a file's
// own time and location decide its directory — except for a best-effort
// capture group (see captureDirs): a sidecar or RAW+JPG pair sharing a
// filename stem and an agreeing EXIF timestamp is forced into one shared
// directory, since a sidecar has no EXIF of its own and would otherwise
// scatter by file mtime. Camera filename counters get reused across
// unrelated shoots, so a bare stem match is never enough on its own — that's
// what the timestamp agreement guards against. A real Live Photo pair still
// lands together because its members genuinely share GPS and timestamp.
func (v *VFS) buildTargets(masters []masterFile) {
	skip := v.uninformativeLevels(masters)
	groupDirs := v.captureDirs(masters, skip)
	taken := map[string]bool{}
	for i := range masters {
		dir, ok := groupDirs[i]
		if !ok {
			dir = v.dirFor(&masters[i], skip)
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
//
// The candidate key is same source directory + same captureStem. A group
// only forms when every member that has a real EXIF timestamp agrees to the
// second: camera filename counters get reused across unrelated shoots (the
// reason the old unconditional stem-pairing was removed), and two people's
// overlapping series landing in the same folder (e.g. AirDrop) must not get
// merged just because a counter happens to collide. A sidecar has no EXIF
// timestamp of its own to agree or disagree with, so it doesn't vote — it
// rides along on whatever its real-timestamped siblings agree on. Video is
// excluded entirely: a Live Photo's .MOV already lands next to its .HEIC
// because they share the same GPS/timestamp (see buildTargets), and forcing
// it into the group's dir could push it across the Photos/Videos split.
func (v *VFS) captureDirs(masters []masterFile, skip map[string]bool) map[int]string {
	type group struct{ members []int }
	groups := map[string]*group{}
	for i := range masters {
		if masters[i].MediaType == classifier.MediaTypeVideo {
			continue
		}
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
		dir := v.dirFor(&masters[leader], skip)
		for _, i := range g.members {
			dirs[i] = dir
		}
	}
	return dirs
}

// dirFor derives the directory segments for one master, honouring Rules
// order. skip names the levels uninformativeLevels found nothing to say with.
func (v *VFS) dirFor(m *masterFile, skip map[string]bool) string {
	if m.takenAt.IsZero() {
		return SanitizeSegment(v.cfg.Fallback)
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

	for _, level := range v.cfg.Rules {
		if skip[level] {
			continue
		}
		seg := v.segmentFor(m, level)
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
func (v *VFS) segmentFor(m *masterFile, level string) string {
	switch level {
	case RuleLocation:
		// HomeWorkDateOnly: an everyday place gets no location folder, just
		// the (possibly merged) date range — m.location itself stays real,
		// it's only the folder that's suppressed.
		if m.atHomeWork && v.cfg.HomeWorkDateOnly {
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
//
// Measured library-wide, not per-branch: a level kept under one Day and dropped
// under the next gives the tree a different depth depending on where you stand.
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

// persist replaces the whole previous proposal — one live set for the library,
// whichever session wrote it. The delete goes through the same FIFO writer as
// the inserts, so a rebuild leaves no stale rows behind.
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

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// caseName applies the configured case style to a derived name. Title case
// uppercases the first letter of every word and lowercases the rest.
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

// deviceCaseExceptions are device-name words a vendor already writes
// correctly cased in its own marketing — title-casing "iPhone" word-by-word
// would give "Iphone", losing the recognizable brand name. Matched
// case-insensitively per word; extend as more of these turn up.
var deviceCaseExceptions = map[string]string{
	"iphone": "iPhone",
}

// caseDevice title-cases a device name word by word ("samsung galaxy s23" ->
// "Samsung Galaxy S23"), keeping deviceCaseExceptions words as-is. The
// hyphen join happens later, in SanitizeSegment, same as every other
// space-separated segment.
func caseDevice(name string) string {
	words := strings.Fields(name)
	for i, w := range words {
		if exact, ok := deviceCaseExceptions[strings.ToLower(w)]; ok {
			words[i] = exact
			continue
		}
		words[i] = caseName(w, CaseTitle)
	}
	return strings.Join(words, " ")
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
// Spaces and commas — routine in a geocoded place name ("Seoni, Himachal
// Pradesh") or a title-cased device name ("Samsung Galaxy S23") — become
// '-', since folder names never carry either; the comma is still fine
// anywhere a person is *choosing* a name (search results, the rename
// dropdown), just not in the name once picked.
func SanitizeSegment(seg string) string {
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
