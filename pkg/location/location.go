// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package location

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	_ "modernc.org/sqlite"
)

var ErrNoLocation = errors.New("locationResolver: location not found")

// MaxDistSquared rejects a nearest-neighbour match beyond ~50 km, as squared
// Euclidean distance in degree-space (sqrt(0.2025) = 0.45°). Exported so
// vfs.resolveLocations folds a city into a home/work anchor at the same reach
// instead of picking its own number.
const MaxDistSquared = 0.2025

// gpsRoundingFactor rounds coordinates to 4 decimal places (≈ 11 m)
const gpsRoundingFactor = 10000

// Bounding-box half-widths queryNearest tries, in order: a tight pass that
// covers most intra-city lookups, then a wide one for rural or coastal photos.
// farSearchDegrees is sqrt(MaxDistSquared) — the widest box must match the
// acceptance radius, or a valid match the wide pass finds is thrown away by a
// tighter threshold.
const (
	NearSearchDegrees = 0.09 // ≈ 10 km
	farSearchDegrees  = 0.45 // ≈ 50 km
)

type cacheKey struct {
	lat, lon float64
}

type Resolver struct {
	db    *db.DB
	cache sync.Map
	log   logger.Logger
}

// NewResolver wraps an already-open, already-verified location database.
// Downloading and verifying that database is pkg/install's job (see
// install.OpenLocationResolver) — this package only ever queries it.
func NewResolver(locationDB *db.DB, log logger.Logger) *Resolver {
	return &Resolver{db: locationDB, log: log}
}

// Lookup returns the name of the nearest populated place for the given
// decimal-degree coordinates
func (r *Resolver) Lookup(ctx context.Context, lat, lon float64) (string, error) {
	// round to ~11 m so photos from one spot share a cache key despite GPS jitter
	key := cacheKey{
		lat: math.Round(lat*gpsRoundingFactor) / gpsRoundingFactor,
		lon: math.Round(lon*gpsRoundingFactor) / gpsRoundingFactor,
	}

	if val, ok := r.cache.Load(key); ok {
		return val.(string), nil
	}

	city, err := r.queryNearest(ctx, key.lat, key.lon)
	if err != nil {
		return "", err
	}
	r.cache.Store(key, city)
	return city, nil
}

// queryNearest returns the nearest city name to the given coordinates, or
// ErrNoLocation if nothing sits within farSearchDegrees.
func (r *Resolver) queryNearest(ctx context.Context, lat, lon float64) (string, error) {
	// widen the box only when the tight pass finds nothing
	for _, delta := range []float64{NearSearchDegrees, farSearchDegrees} {
		cands, err := r.Candidates(ctx, lat, lon, delta, 1)
		if err != nil {
			return "", err
		}
		if len(cands) == 0 {
			continue
		}
		// DisplayName, not Name: an auto-named folder is qualified the same way
		// the review picker and saved anchors are, so two Hyderabads stay apart
		return cands[0].DisplayName, nil
	}
	return "", ErrNoLocation
}

// Candidate is one ranked reverse-geocode match. Which name to use depends on
// who reads it: a folder named automatically gets DisplayName, a list a person
// picks from shows FullName.
type Candidate struct {
	Name        string // plain city, no qualifier: "Springfield"
	DisplayName string // smallest unique qualifier: "Springfield, Illinois"
	FullName    string // spelled out: "Springfield, Illinois, United States"
	DistKM      float64
	hasMarks    bool // the gazetteer entry carried diacritics stripDiacritics removed
}

// candidateFetchLimit over-fetches so Candidates can prefer a plain-spelled
// entry over a diacritic one at roughly the same distance.
const candidateFetchLimit = 32

// searchOverfetchFactor over-fetches in SearchByName, whose qualifier and
// duplicate filtering runs after the query — limiting in SQL could return a
// page made entirely of rows that don't survive it.
const searchOverfetchFactor = 8

// nameCountsSQL counts, for each row: cities sharing this name, the countries
// they span, and how many sit in this row's own country. disambiguate turns
// the three into the smallest unique qualifier. All hit idx_geonames_cities_city.
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

// Candidates returns up to limit matches within deltaDegrees, nearest first,
// but plain-spelled entries always ahead of diacritic ones. Used by the review
// TUI to offer alternatives when Lookup's top pick is wrong.
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

// ResolveByName forward-geocodes a saved place name to coordinates. Exact
// case-insensitive match first, then a diacritic-stripped pass: this package
// only ever hands out stripped names, so "Banjār" is saved as "Banjar".
func (r *Resolver) ResolveByName(ctx context.Context, name string) (lat, lon float64, err error) {
	// honour the qualifiers: matching the bare city alone would resolve
	// "Hyderabad, India" to whichever row comes back first, the Pakistani one
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

// resolveStripped is ResolveByName's fallback for diacritic entries. NOCASE is
// ASCII-only and there is no stripped column to index, so this scans.
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

// PlaceMatch is one gazetteer entry matching a typed prefix, with coordinates
// so a caller needs no second lookup. The three names mean what they do on
// Candidate; ResolveByName resolves any of them.
type PlaceMatch struct {
	Name        string
	DisplayName string
	FullName    string
	Lat, Lon    float64
}

// SearchByName finds gazetteer entries starting with prefix, so a picker offers
// DB-backed names instead of free text that might resolve to nothing later.
func (r *Resolver) SearchByName(ctx context.Context, prefix string, limit int) ([]PlaceMatch, error) {
	// a typed name may already carry its qualifier — that is what the picker
	// offered and what an anchor is saved as, so it has to find the same row
	city, qualifiers := splitQualified(prefix)
	fetch := limit * searchOverfetchFactor
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
		// the gazetteer has near-duplicates a few hundred metres apart; listing
		// one string twice isn't a choice. ORDER BY makes "the first" stable.
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
// apart:
//
//	unique name                  -> "Springfield"
//	several in this country      -> "Springfield, Illinois"  (state)
//	one here, more abroad        -> "Springfield, Australia" (country)
//
// Two entries with the same name in the same state stay identical: the
// gazetteer has genuine near-duplicates, and a third qualifier would lengthen
// every folder to fix one of them.
func disambiguate(city, state, country string, nameCount, countryCount, inCountryCount int) string {
	if nameCount <= 1 {
		return city
	}
	// state even when the name also occurs abroad — no other Springfield in
	// Illinois exists to collide with, and a state is what a reader recognizes
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

// splitQualified splits any of the three name forms into the city and the
// qualifiers after it: "Hyderabad, Telangana, India" -> city plus two, each of
// which must then match a row's state or country.
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

// stripDiacritics normalizes a gazetteer name ("Banjār") to plain ASCII
// ("Banjar") — combining marks read as typos in an unfamiliar script.
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
