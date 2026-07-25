// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package location

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/sync/singleflight"
	"golang.org/x/text/unicode/norm"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/utils"
	_ "modernc.org/sqlite"
)

var ErrNoLocation = errors.New("locationResolver: location not found")

// MaxDistSquared is the rejection threshold for the nearest-neighbour search,
// expressed as squared Euclidean distance in degree-space
// sqrt(0.2025) = 0.45° ≈ 50 km — matches the outer bounding box below so a
// match the wide pass finds isn't then thrown away by a tighter threshold
// (that mismatch was silently fragmenting locations: photos 15-40km from the
// nearest named place fell back to no location at all instead of the nearest
// match). Exported so vfs.resolveLocations can reuse the same reach for
// folding a resolved city into a home/work anchor, instead of picking its own
// number.
const MaxDistSquared = 0.2025

// gpsRoundingFactor rounds coordinates to 4 decimal places (≈ 11 m)
const gpsRoundingFactor = 10000

type cacheKey struct {
	lat, lon float64
}

type Resolver struct {
	db    *db.DB
	cache sync.Map
	sf    singleflight.Group
	log   logger.Logger
}

type locationMeta struct {
	Hash      string         `json:"sha256"`
	Version   string         `json:"version"`
	UpdatedAt time.Time      `json:"updated_at"`
	Rows      map[string]int `json:"rows"`
}

func New(locationDB *db.DB, dbLocationPath string, log logger.Logger) (*Resolver, error) {
	metaPath := filepath.Join(filepath.Dir(dbLocationPath), LocationMetaFileName)

	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read location meta: %w", err)
	}

	var meta locationMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("unable to parse location meta: %w", err)
	}

	sum, err := utils.SHA256File(dbLocationPath)
	if err != nil {
		return nil, fmt.Errorf("checksum location db: %w", err)
	}
	if sum != meta.Hash {
		return nil, fmt.Errorf("location db checksum mismatch: got %s, want %s", sum, meta.Hash)
	}
	// Deliberately not UserKey-tagged: New runs on every command that opens the
	// resolver (config, scan, serve, review), so tagging it printed a checksum
	// line on the console every single run. It's an integrity check, not a
	// milestone — Setup already prints a user-facing line on the one run that
	// actually downloads anything. Failures above are still surfaced as errors.
	log.Info("location db checksum verified", "path", dbLocationPath, "hash", sum)

	var count int
	err = locationDB.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM geonames_cities`,
	).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("verifying location database: %w", err)
	}

	if count != meta.Rows["geonames_cities"] {
		return nil, fmt.Errorf("row count mismatch: db has %d, meta expects %d", count, meta.Rows["geonames_cities"])
	}

	log.Info("location db verified", "path", dbLocationPath, "rows", count)
	return &Resolver{db: locationDB, log: log}, nil
}

// Lookup returns the name of the nearest populated place for the given
// decimal-degree coordinates
func (r *Resolver) Lookup(ctx context.Context, lat, lon float64) (string, error) {
	// Round to 4 decimal places ≈ 11 m precision — close enough that photos
	// from the same physical spot
	// Formula: round(x * gpsRoundingFactor) / gpsRoundingFactor keeps values stable across minor GPS jitter
	key := cacheKey{
		lat: math.Round(lat*gpsRoundingFactor) / gpsRoundingFactor,
		lon: math.Round(lon*gpsRoundingFactor) / gpsRoundingFactor,
	}

	// 1. Load: Returns value and a boolean 'ok'
	if val, ok := r.cache.Load(key); ok {
		return val.(string), nil
	}

	// 2. Singleflight: Protect against cache stampede
	val, err, _ := r.sf.Do(fmt.Sprintf("%f:%f", key.lat, key.lon), func() (any, error) {
		// Re-check cache in case another goroutine just finished the DB call
		if val, ok := r.cache.Load(key); ok {
			return val, nil
		}

		city, err := r.queryNearest(ctx, key.lat, key.lon)
		if err != nil {
			return "", err
		}

		// 3. Store: Cache the result
		r.cache.Store(key, city)
		return city, nil
	})

	if err != nil {
		return "", err
	}
	return val.(string), nil
}

// queryNearest finds the closest city name to the given coordinates
//
// It queries using an expanding bounding box, then ranks candidates by squared
// Euclidean distance in degree-space, and returns the nearest match
//
// Approximate real-world search radii:
//
//	Start: ~10 km  (±0.09°)
//	End:   ~50 km  (±0.45°)
//
// Returns ErrNoLocation if no candidate is found within either box, or if the
// closest match exceeds MaxDistSquared
func (r *Resolver) queryNearest(ctx context.Context, lat, lon float64) (string, error) {
	// deltaDegrees lists the bounding-box half-widths to try in order
	//   0.09° ≈ 10 km — tight first pass, covers most intra-city lookups
	//   0.45° ≈ 50 km — wider fallback for rural or coastal photos
	for _, delta := range []float64{0.09, 0.45} {
		cands, err := r.Candidates(ctx, lat, lon, delta, 1)
		if err != nil {
			return "", err
		}
		if len(cands) == 0 {
			continue
		}
		// DisplayName, not Name: the folder a photo lands in automatically is
		// named the same way the review picker and the saved anchors name it,
		// so a library that saw both Hyderabads doesn't merge them into one
		// folder. Unique names carry no qualifier, so most folders are unchanged.
		return cands[0].DisplayName, nil
	}
	return "", ErrNoLocation
}

// Candidate is one ranked reverse-geocode match.
//
//	Name        plain city name, no qualifier ("Springfield")
//	DisplayName smallest qualifier that makes it unique — what an automatically
//	            named folder gets ("Springfield, Illinois")
//	FullName    city, state and country spelled out — what a person picks from
//	            ("Springfield, Illinois, United States")
type Candidate struct {
	Name        string
	DisplayName string
	FullName    string
	DistKM      float64
	hasMarks    bool // the raw gazetteer entry carried diacritics stripDiacritics removed
}

// candidateFetchLimit over-fetches so Candidates has enough rows to prefer a
// plain-spelled entry over a diacritic one at roughly the same location,
// rather than just returning whichever the distance sort happened to put first.
const candidateFetchLimit = 32

// nameCountsSQL are the three correlated subqueries every name-producing query
// needs: how many cities share this name, across how many countries, and how
// many of them sit in *this* row's country. disambiguate turns them into the
// smallest qualifier that makes the folder name unique. All hit
// idx_geonames_cities_city.
const nameCountsSQL = `
    	(SELECT COUNT(*)                        FROM geonames_cities g2 WHERE g2.city = gc.city COLLATE NOCASE),
    	(SELECT COUNT(DISTINCT g2.country_code) FROM geonames_cities g2 WHERE g2.city = gc.city COLLATE NOCASE),
    	(SELECT COUNT(*)                        FROM geonames_cities g2 WHERE g2.city = gc.city COLLATE NOCASE AND g2.country_code = gc.country_code)`

// candidateQuery is shared by Candidates and queryNearest.
var candidateQuery = `
	WITH params AS (
    SELECT ? AS lat, ? AS lon, ? AS delta
	)
	SELECT gc.city,
    	(gc.latitude  - p.lat) * (gc.latitude  - p.lat) +
    	(gc.longitude - p.lon) * (gc.longitude - p.lon) AS dist,
    	COALESCE(gc.state, ''), COALESCE(gc.country, ''),` + nameCountsSQL + `
	FROM   geonames_cities gc, params p
	WHERE  gc.latitude  BETWEEN p.lat - p.delta AND p.lat + p.delta
	AND    gc.longitude BETWEEN p.lon - p.delta AND p.lon + p.delta
	ORDER  BY dist
	LIMIT  ?`

// kmPerDegree is a rough degree-to-km conversion for the DistKM estimate shown
// to the user; the search itself stays in degree-space (see MaxDistSquared)
const kmPerDegree = 111.0

// Candidates returns up to limit reverse-geocode matches within deltaDegrees
// of the given coordinates. Plain-spelled entries (no diacritics) rank ahead
// of a diacritic entry even if the latter is a hair closer — a diacritic
// variant is only offered when no plain entry exists in range at all — and
// within each group, nearest first. Used by the review TUI to offer ranked
// alternatives when the top pick (Lookup) is wrong.
func (r *Resolver) Candidates(ctx context.Context, lat, lon, deltaDegrees float64, limit int) ([]Candidate, error) {
	rows, err := r.db.QueryContext(ctx, candidateQuery, lat, lon, deltaDegrees, candidateFetchLimit)
	if err != nil {
		return nil, fmt.Errorf("locationResolver: query: %w", err)
	}
	defer rows.Close()

	var out []Candidate
	for rows.Next() {
		var city, state, country string
		var distSq float64
		var nameCnt, countryCnt, inCountryCnt int
		if err := rows.Scan(&city, &distSq, &state, &country, &nameCnt, &countryCnt, &inCountryCnt); err != nil {
			return nil, fmt.Errorf("locationResolver: scan: %w", err)
		}
		if distSq > MaxDistSquared {
			continue
		}
		plain := stripDiacritics(city)
		out = append(out, Candidate{
			Name:        plain,
			DisplayName: disambiguate(plain, state, country, nameCnt, countryCnt, inCountryCnt),
			FullName:    fullName(plain, state, country),
			DistKM:      math.Sqrt(distSq) * kmPerDegree,
			hasMarks:    plain != city,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].hasMarks != out[j].hasMarks {
			return !out[i].hasMarks // plain-spelled entries sort first
		}
		return out[i].DistKM < out[j].DistKM
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ResolveByName forward-geocodes a typed place name (e.g. a hometown) to
// coordinates, for storing as an anchor label. Tries an exact, case-insensitive
// match first, then falls back to comparing diacritic-stripped names: every
// name this package hands out is stripped (SearchByName, Candidates), so a
// gazetteer entry spelled "Banjār" is only ever saved as "Banjar" and the exact
// match alone would never find its way back.
func (r *Resolver) ResolveByName(ctx context.Context, name string) (lat, lon float64, err error) {
	// The name can be any of the three forms the picker hands out, so the
	// qualifiers after the city have to be honoured: matching the bare city
	// alone would resolve "Hyderabad, India" to whichever Hyderabad the DB
	// returns first — the one in Pakistan.
	city, qualifiers := splitQualified(name)
	rows, err := r.db.QueryContext(ctx,
		`SELECT COALESCE(state, ''), COALESCE(country, ''), latitude, longitude
		 FROM geonames_cities WHERE city = ? COLLATE NOCASE`, city)
	if err != nil {
		return 0, 0, fmt.Errorf("locationResolver: resolve by name: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var state, country string
		var clat, clon float64
		if err := rows.Scan(&state, &country, &clat, &clon); err != nil {
			return 0, 0, fmt.Errorf("locationResolver: scan: %w", err)
		}
		if matchesQualifiers(state, country, qualifiers) {
			return clat, clon, nil
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	return r.resolveStripped(ctx, city, qualifiers)
}

// resolveStripped is ResolveByName's fallback for names whose gazetteer entry
// carries diacritics. SQLite's NOCASE collation is ASCII-only and there's no
// stripped column to index, so this scans.
// ponytail: full table scan, run at most twice per scan (home + work anchor)
// and only when the exact match missed. Add a normalized column + index if it
// ever lands on a hot path.
func (r *Resolver) resolveStripped(ctx context.Context, name string, qualifiers []string) (lat, lon float64, err error) {
	want := strings.ToLower(stripDiacritics(name))
	rows, err := r.db.QueryContext(ctx,
		`SELECT city, COALESCE(state, ''), COALESCE(country, ''), latitude, longitude FROM geonames_cities`)
	if err != nil {
		return 0, 0, fmt.Errorf("locationResolver: resolve by name: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var city, state, country string
		var clat, clon float64
		if err := rows.Scan(&city, &state, &country, &clat, &clon); err != nil {
			return 0, 0, fmt.Errorf("locationResolver: scan: %w", err)
		}
		if strings.ToLower(stripDiacritics(city)) == want && matchesQualifiers(state, country, qualifiers) {
			return clat, clon, nil
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	return 0, 0, ErrNoLocation
}

// PlaceMatch is one gazetteer entry matching a typed prefix, coordinates
// included so a caller can use the selection directly without a second lookup.
// Name/DisplayName/FullName mean the same as on Candidate: the picker shows
// FullName, an anchor is saved as the string the user actually picked, and
// ResolveByName can resolve any of the three.
type PlaceMatch struct {
	Name        string
	DisplayName string
	FullName    string
	Lat, Lon    float64
}

// SearchByName finds gazetteer entries whose name starts with prefix, so an
// interactive picker (e.g. the config wizard's town fields) can offer exact DB-backed
// names instead of free text that might not match anything — the picked name
// can never drift from what's actually in the location DB.
func (r *Resolver) SearchByName(ctx context.Context, prefix string, limit int) ([]PlaceMatch, error) {
	// A typed name may already carry its qualifier ("Hyderabad, India") — that's
	// what the picker offered and what an anchor is saved as, so searching for
	// it again has to find its way back to the same row.
	city, qualifiers := splitQualified(prefix)
	// Over-fetch: qualifiers and duplicate full names are filtered below, so
	// limiting in SQL could return a page made entirely of rows that don't
	// survive — or of the wrong Springfields.
	fetch := limit * 8
	rows, err := r.db.QueryContext(ctx,
		`SELECT gc.city, gc.latitude, gc.longitude,
		        COALESCE(gc.state, ''), COALESCE(gc.country, ''),`+nameCountsSQL+`
		 FROM geonames_cities gc
		 WHERE gc.city LIKE ? || '%' COLLATE NOCASE
		 ORDER BY gc.city LIMIT ?`,
		city, fetch)
	if err != nil {
		return nil, fmt.Errorf("locationResolver: search: %w", err)
	}
	defer rows.Close()

	var out []PlaceMatch
	seen := map[string]bool{}
	for rows.Next() {
		var name, state, country string
		var lat, lon float64
		var nameCnt, countryCnt, inCountryCnt int
		if err := rows.Scan(&name, &lat, &lon, &state, &country, &nameCnt, &countryCnt, &inCountryCnt); err != nil {
			return nil, fmt.Errorf("locationResolver: scan: %w", err)
		}
		if !matchesQualifiers(state, country, qualifiers) {
			continue
		}
		plain := stripDiacritics(name)
		// The gazetteer carries near-duplicates (two "Banjar, West Java,
		// Indonesia" a few hundred metres apart). Listing the same string twice
		// isn't a choice — keep the first, which the ORDER BY makes stable.
		if full := fullName(plain, state, country); seen[full] {
			continue
		} else {
			seen[full] = true
		}
		out = append(out, PlaceMatch{
			Name:        plain,
			DisplayName: disambiguate(plain, state, country, nameCnt, countryCnt, inCountryCnt),
			FullName:    fullName(plain, state, country),
			Lat:         lat, Lon: lon,
		})
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, rows.Err()
}

// disambiguate returns the smallest qualifier that tells same-named cities
// apart, per entry:
//
//	unique name                     -> "Springfield"
//	several in this same country    -> "Springfield, Illinois"   (state)
//	one here, more abroad           -> "Springfield, Australia"  (country)
//
// The in-country case takes the state even when the name also occurs abroad:
// state names are what a person recognizes, and no other Springfield in
// Illinois exists to collide with. Two entries for the same name in the same
// state are left identical — the gazetteer has genuine near-duplicates and a
// third qualifier would only make every folder longer.
func disambiguate(city, state, country string, nameCount, countryCount, inCountryCount int) string {
	if nameCount <= 1 {
		return city
	}
	if inCountryCount > 1 && state != "" {
		return city + ", " + stripDiacritics(state)
	}
	if countryCount > 1 && country != "" {
		return city + ", " + stripDiacritics(country)
	}
	if state != "" {
		return city + ", " + stripDiacritics(state)
	}
	return city
}

// fullName spells a place out for a person choosing from a list: city, state
// and country, skipping the parts the gazetteer doesn't have. Two same-named
// cities are never ambiguous here, whatever the disambiguate ladder decided
// was enough for a folder name.
func fullName(city, state, country string) string {
	parts := []string{city}
	for _, p := range []string{state, country} {
		if p = stripDiacritics(strings.TrimSpace(p)); p != "" && p != parts[len(parts)-1] {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ", ")
}

// splitQualified splits any of the three name forms back into the city and the
// qualifiers after it: "Hyderabad" -> ("Hyderabad", nil), "Hyderabad, India"
// and "Hyderabad, Telangana, India" -> the city plus one or two qualifiers,
// each of which must match that row's state or country (matchesQualifiers).
func splitQualified(name string) (city string, qualifiers []string) {
	parts := strings.Split(strings.TrimSpace(name), ",")
	city = strings.TrimSpace(parts[0])
	for _, p := range parts[1:] {
		if p = strings.TrimSpace(p); p != "" {
			qualifiers = append(qualifiers, p)
		}
	}
	return city, qualifiers
}

// matchesQualifiers reports whether a row's state/country account for every
// qualifier in a picked name. No qualifiers matches anything, so a bare name
// saved before qualifiers existed still resolves.
func matchesQualifiers(state, country string, qualifiers []string) bool {
	state, country = strings.ToLower(stripDiacritics(state)), strings.ToLower(stripDiacritics(country))
	for _, q := range qualifiers {
		q = strings.ToLower(stripDiacritics(q))
		if q != state && q != country {
			return false
		}
	}
	return true
}

// stripDiacritics normalizes a raw gazetteer name ("Banjār") to its plain
// ASCII form ("Banjar") — some entries carry combining marks that read as
// typos to users unfamiliar with the source script.
func stripDiacritics(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return norm.NFC.String(b.String())
}
