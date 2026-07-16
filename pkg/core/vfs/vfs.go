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
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

// metadataExtractor is the exiftool seam; *exiftool.Extractor satisfies it
type metadataExtractor interface {
	Extract(ctx context.Context, path string) (classifier.CommonMetadata, error)
}

// geoResolver is the reverse-geocode seam; *location.Resolver satisfies it
type geoResolver interface {
	Lookup(ctx context.Context, lat, lon float64) (string, error)
}

type VFS struct {
	db       *db.DB
	resolver geoResolver
	log      logger.Logger
	extract  metadataExtractor
	path     *path.Resolver
	cfg      Config
}

func New(database *db.DB, resolver *location.Resolver, log logger.Logger, exiftoolPath string, cfg Config) *VFS {
	v := &VFS{
		db:      database,
		log:     log,
		extract: exiftool.New(exiftoolPath),
		path:    path.New(),
		cfg:     cfg,
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

	v.enrichAll(ctx, masters)
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	v.resolveLocations(ctx, masters)
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

// loadMasters reads every master file in the library joined with its hashed
// metadata. Deliberately not session-scoped: the proposal must cover files
// indexed by earlier sessions too, or the output would depend on session
// history. The (source_root, file_path) order makes clustering and collision
// suffixes deterministic (clusterAndSuggest sorts stably), unlike
// AUTOINCREMENT ids which vary with concurrent-worker insertion order
func (v *VFS) loadMasters(ctx context.Context) ([]masterFile, error) {
	var masters []masterFile
	if err := v.db.SQL.SelectContext(ctx, &masters, `
		SELECT fr.id, fr.file_path, fr.source_root, fr.media_type, fr.file_extension, fr.file_modified_at,
			fm.exif_image_width, fm.exif_image_height, fm.exif_gps_latitude, fm.exif_gps_longitude,
			fm.exif_make, fm.exif_model, fm.exif_date_time_original, fm.exif_create_date
		FROM file_registry fr
		JOIN file_metadata fm ON fm.file_id = fr.id
		WHERE fm.is_master = 1
		ORDER BY fr.source_root, fr.file_path`); err != nil {
		return nil, fmt.Errorf("query master files: %w", err)
	}
	for i := range masters {
		masters[i].absPath = v.path.MakeAbsolute(masters[i].FilePath, masters[i].SourceRoot)
	}
	return masters, nil
}

func (v *VFS) loadLabels(ctx context.Context) ([]userLabel, error) {
	var labels []userLabel
	if err := v.db.SQL.SelectContext(ctx, &labels,
		`SELECT label, kind, time_start, time_end FROM user_labels`); err != nil {
		return nil, fmt.Errorf("query user labels: %w", err)
	}
	return labels, nil
}

// enrichAll re-extracts EXIF from every master file on disk with a bounded
// worker pool. Fresh extraction is required because hashing persists only a
// small subset of the tags the VFS needs
func (v *VFS) enrichAll(ctx context.Context, masters []masterFile) {
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range v.cfg.Workers {
		wg.Go(func() {
			for i := range jobs {
				if ctx.Err() != nil {
					continue // drain remaining jobs without work
				}
				v.enrich(ctx, &masters[i])
			}
		})
	}

feed:
	for i := range masters {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
}

// enrich fills the derived fields of a single master from fresh EXIF,
// falling back to the metadata persisted during hashing
func (v *VFS) enrich(ctx context.Context, m *masterFile) {
	meta, err := v.extract.Extract(ctx, m.absPath)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		v.log.Warn("EXIF extraction failed; using stored metadata", "path", m.absPath, "error", err)
	}

	m.takenAt = firstTime(meta.DateTimeOriginal, meta.CreateDate,
		deref(m.DBDateTaken), deref(m.DBCreateDate), m.ModifiedAt)
	m.width = firstInt(meta.ImageWidth, m.DBWidth)
	m.height = firstInt(meta.ImageHeight, m.DBHeight)
	// EXIF orientations 5-8 mean the stored pixels are rotated 90°/270°;
	// swap so the orientation slot reflects how the shot is viewed
	if o, ok := parseFloat(meta.Orientation); ok && o >= 5 && o <= 8 {
		m.width, m.height = m.height, m.width
	}

	if lat, ok := parseFloat(meta.GPSLatitude); ok {
		if lon, ok := parseFloat(meta.GPSLongitude); ok {
			m.hasGPS, m.lat, m.lon = true, lat, lon
		}
	}
	if !m.hasGPS && m.DBLat != nil && m.DBLon != nil {
		m.hasGPS, m.lat, m.lon = true, *m.DBLat, *m.DBLon
	}

	m.device = deviceName(
		firstStr(meta.Make, deref(m.DBMake)),
		firstStr(meta.Model, deref(m.DBModel)),
	)
}

// resolveLocations reverse-geocodes every GPS-tagged master.
// ponytail: serial loop — the resolver caches and singleflights internally;
// parallelise only if geocoding ever shows up in profiles
func (v *VFS) resolveLocations(ctx context.Context, masters []masterFile) {
	if v.resolver == nil {
		return
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

// buildTargets derives the destination path for every master. Capture-group
// members (Live Photo pairs, RAW+JPG, sidecars) are grouped on the fly and
// always land in the directory derived from the group's original member
func (v *VFS) buildTargets(masters []masterFile) {
	type group struct {
		leader  int   // index of the ORIGINAL member (or first seen)
		members []int // indices into masters
	}

	var groups []*group
	index := map[string]*group{}
	for i := range masters {
		m := &masters[i]
		info := scanner.DeriveCapture(filepath.Base(m.FilePath), strings.ToLower(m.Extension), m.MediaType)
		// same source directory + same capture stem = same capture event
		key := m.SourceRoot + "|" + filepath.Dir(m.FilePath) + "|" + info.Key
		g, ok := index[key]
		if !ok {
			g = &group{leader: i}
			index[key] = g
			groups = append(groups, g)
		}
		if info.Role == scanner.CaptureRoleOriginal {
			g.leader = i
		}
		g.members = append(g.members, i)
	}

	// collisions: the suffix is decided per capture group so members keep a
	// common stem and move together. Every final path is reserved, so a
	// suffixed name can't collide with a stem that already ends in _N
	memberPath := func(i int, dir, suffix string) string {
		name := filepath.Base(masters[i].FilePath)
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		return dir + "/" + stem + suffix + filepath.Ext(name)
	}
	taken := map[string]bool{}
	for _, g := range groups {
		dir := v.dirFor(&masters[g.leader])
		suffix := ""
		for n := 1; ; n++ {
			if n > 1 {
				suffix = fmt.Sprintf("_%d", n)
			}
			free := true
			for _, i := range g.members {
				if taken[strings.ToLower(memberPath(i, dir, suffix))] {
					free = false
					break
				}
			}
			if free {
				break
			}
		}
		for _, i := range g.members {
			p := memberPath(i, dir, suffix)
			masters[i].targetPath = p
			taken[strings.ToLower(p)] = true
		}
	}
}

// dirFor derives the directory segments for one master, honouring slot order
func (v *VFS) dirFor(m *masterFile) string {
	if m.takenAt.IsZero() {
		// ponytail: no usable timestamp at all (should never happen — file
		// mtime is always present); park flat under the fallback dir
		return sanitizeSegment(v.cfg.Fallback)
	}

	parts := []string{
		strconv.Itoa(m.takenAt.Year()),
		fmt.Sprintf("%02d_%s", int(m.takenAt.Month()), m.takenAt.Month()),
	}
	for _, slot := range v.cfg.Slots {
		seg := ""
		switch slot {
		case SlotLocation:
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
		case SlotDevice:
			seg = m.device // skipped when unknown
		case SlotOrientation:
			if m.width > 0 && m.height > 0 { // skipped when dimensions unknown
				if m.height > m.width {
					seg = "Vertical"
				} else {
					seg = "Horizontal"
				}
			}
		case SlotMedia:
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

func parseFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

func firstInt(fresh string, stored *int64) int64 {
	if f, ok := parseFloat(fresh); ok {
		return int64(f)
	}
	if stored != nil {
		return *stored
	}
	return 0
}

func firstStr(candidates ...string) string {
	for _, c := range candidates {
		if strings.TrimSpace(c) != "" {
			return strings.TrimSpace(c)
		}
	}
	return ""
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
