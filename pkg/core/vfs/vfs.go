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
	for _, l := range labels {
		if (l.Kind == "ANCHOR_HOME" || l.Kind == "ANCHOR_WORK") && l.GPSLat != nil && l.GPSLon != nil {
			anchors = append(anchors, l)
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
				m.location = a.Label
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
	taken := map[string]bool{}
	for i := range masters {
		dir := v.dirFor(&masters[i])
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

// dirFor derives the directory segments for one master, honouring GroupBy order
func (v *VFS) dirFor(m *masterFile) string {
	if m.takenAt.IsZero() {
		// ponytail: no usable timestamp at all (should never happen — file
		// mtime is always present); park flat under the fallback dir
		return sanitizeSegment(v.cfg.Fallback)
	}

	parts := []string{
		strconv.Itoa(m.takenAt.Year()),
		m.takenAt.Month().String(),
	}
	for _, level := range v.cfg.GroupBy {
		seg := ""
		switch level {
		case GroupByLocation:
			// ladder: resolved city → dated event segment → device → fallback
			switch {
			case m.location != "":
				seg = m.location
			case m.eventSegment != "":
				seg = m.eventSegment
			case m.device != "":
				seg = m.device
			default:
				seg = v.cfg.Fallback
			}
		case GroupByDevice:
			seg = m.device // skipped when unknown
		case GroupByOrientation:
			if m.width > 0 && m.height > 0 { // skipped when dimensions unknown
				if m.height > m.width {
					seg = "Vertical"
				} else {
					seg = "Horizontal"
				}
			}
		case GroupByMedia:
			if m.MediaType == classifier.MediaTypeVideo {
				seg = "Videos"
			} else {
				seg = "Photos"
			}
		}
		if seg != "" {
			parts = append(parts, sanitizeSegment(seg))
		}
	}
	return strings.Join(parts, "/")
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
					(session_id, file_id, source_path, target_path, cluster_id, status, suggestion, suggestion_source)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				sessionID.String(), m.FileID, m.absPath, m.targetPath,
				nullable(m.clusterID), db.StatusProposed,
				nullable(m.suggestion), nullable(m.suggestionSource)); err != nil {
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
