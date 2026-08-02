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

// MaxDistSquared rejects a match beyond ~50km (squared degree-space distance).
// Exported: vfs.resolveLocations reuses it for the anchor-fold radius.
const MaxDistSquared = 0.2025

// gpsRoundingFactor rounds coordinates to 2 decimal places (≈ 1.1 km) for the
// Lookup cache key. The grid only has to be small against the acceptance radius
// (MaxDistSquared, ~50 km) to name the same city, and it decides how often a
// walk around town pays for a fresh query: an 11 m grid billed one every eleven
// metres.
const gpsRoundingFactor = 100

// Bounding-box half-widths queryNearest tries in order: tight first, then wide.
const (
	NearSearchDegrees = 0.09 // ≈ 10 km
	// farSearchDegrees is sqrt(MaxDistSquared) — must match the acceptance
	// radius or a valid wide-pass match gets thrown away by a tighter threshold.
	farSearchDegrees = 0.45 // ≈ 50 km
)

type cacheKey struct {
	lat, lon float64
}

type Resolver struct {
	db    *db.DB
	cache sync.Map
	log   logger.Logger
	// Anchors are saved places resolved to GPS coordinates. Nil until
	// BuildAnchors is called.
	Anchors []Anchor
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
	// round so photos taken around one place share a cache key despite GPS jitter
	key := cacheKey{
		lat: math.Round(lat*gpsRoundingFactor) / gpsRoundingFactor,
		lon: math.Round(lon*gpsRoundingFactor) / gpsRoundingFactor,
	}

	if val, ok := r.cache.Load(key); ok {
		if city := val.(string); city != "" {
			return city, nil
		}
		return "", ErrNoLocation
	}

	city, err := r.queryNearest(ctx, key.lat, key.lon)
	switch {
	// remember that nothing is near this square too — a library shot far from
	// any populated place would otherwise re-run both passes for every file.
	// Only ErrNoLocation: a cancelled context or a failed query says nothing
	// about the coordinates and must stay retryable.
	case errors.Is(err, ErrNoLocation):
		r.cache.Store(key, "")
		return "", err
	case err != nil:
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
	hasMarks    bool // the geonames entry carried diacritics stripDiacritics removed
}

// candidateFetchLimit over-fetches so Candidates can prefer a plain-spelled
// entry over a diacritic one at roughly the same distance.
const candidateFetchLimit = 32

// searchOverfetchFactor over-fetches in SearchByName, whose qualifier and
// duplicate filtering runs after the query — limiting in SQL could return a
// page made entirely of rows that don't survive it.
const searchOverfetchFactor = 8

// candidateQuery is shared by Candidates and queryNearest.
var candidateQuery = `
	WITH params AS (
    SELECT ? AS lat, ? AS lon, ? AS delta
	)
	SELECT gc.city,
    	(gc.latitude  - p.lat) * (gc.latitude  - p.lat) +
    	(gc.longitude - p.lon) * (gc.longitude - p.lon) AS dist,
    	COALESCE(gc.state, ''), COALESCE(gc.country, ''), COALESCE(gc.country_code, '')
	FROM   geonames_cities gc, params p
	WHERE  gc.latitude  BETWEEN p.lat - p.delta AND p.lat + p.delta
	AND    gc.longitude BETWEEN p.lon - p.delta AND p.lon + p.delta
	ORDER  BY dist
	LIMIT  ?`

// nameCounts is what disambiguate needs about one city name: how many geonames
// rows carry it, how many countries those span, and how many sit in each.
type nameCounts struct {
	total     int
	countries int
	inCountry map[string]int
}

// countNames answers those three questions for a handful of city names in one
// indexed GROUP BY. It used to be three correlated subqueries embedded in the
// row query, which ran them for every row fetched — 32 rows × 3 to disambiguate
// the one name queryNearest keeps. Asking only for the names a caller actually
// gets back took a Paris lookup from ~144ms to ~6ms.
func (r *Resolver) countNames(ctx context.Context, cities []string) (map[string]nameCounts, error) {
	want := make([]any, 0, len(cities))
	seen := map[string]bool{}
	for _, c := range cities {
		if k := nocaseKey(c); !seen[k] {
			seen[k] = true
			want = append(want, c)
		}
	}
	out := map[string]nameCounts{}
	if len(want) == 0 {
		return out, nil
	}

	query := `SELECT gc.city, COALESCE(gc.country_code, ''), COUNT(*)
		FROM geonames_cities gc
		WHERE gc.city COLLATE NOCASE IN (?` + strings.Repeat(",?", len(want)-1) + `)
		GROUP BY gc.city COLLATE NOCASE, gc.country_code`
	rows, err := r.db.QueryContext(ctx, query, want...)
	if err != nil {
		return nil, fmt.Errorf("locationResolver: name counts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var city, code string
		var n int
		if err := rows.Scan(&city, &code, &n); err != nil {
			return nil, fmt.Errorf("locationResolver: scan name counts: %w", err)
		}
		c := out[nocaseKey(city)]
		if c.inCountry == nil {
			c.inCountry = map[string]int{}
		}
		// every row carrying the name counts toward the total, but only a real
		// country code is a country — matching COUNT(DISTINCT country_code),
		// which ignores NULLs
		c.total += n
		if code != "" {
			c.countries++
			c.inCountry[code] += n
		}
		out[nocaseKey(city)] = c
	}
	return out, rows.Err()
}

// nocaseKey folds a city name the way SQLite's NOCASE collation does — ASCII
// letters only. strings.ToLower would also fold "Ā" to "ā", merging two groups
// the GROUP BY kept apart.
func nocaseKey(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

// naming turns one row's counts into the two names a caller may want.
func (r *Resolver) naming(counts map[string]nameCounts, city, plain, state, country, code string) (display, full string) {
	c := counts[nocaseKey(city)]
	return disambiguate(plain, state, country, c.total, c.countries, c.inCountry[code], r.cityClaimed(plain), ", "),
		fullName(plain, state, country)
}

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

	// raw rows first: ranking needs only the distance and the spelling, so the
	// name counts are deferred until the list has been cut down to what the
	// caller asked for (see countNames)
	type row struct {
		city, state, country, code string
		distKM                     float64
		hasMarks                   bool
	}
	var raw []row
	for rows.Next() {
		var city, state, country, code string
		var distSq float64
		if err := rows.Scan(&city, &distSq, &state, &country, &code); err != nil {
			return nil, fmt.Errorf("locationResolver: scan: %w", err)
		}
		if distSq > MaxDistSquared {
			continue
		}
		raw = append(raw, row{
			city: city, state: state, country: country, code: code,
			distKM:   math.Sqrt(distSq) * kmPerDegree,
			hasMarks: stripDiacritics(city) != city,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(raw, func(i, j int) bool {
		if raw[i].hasMarks != raw[j].hasMarks {
			return !raw[i].hasMarks // plain-spelled entries sort first
		}
		return raw[i].distKM < raw[j].distKM
	})
	if len(raw) > limit {
		raw = raw[:limit]
	}

	cities := make([]string, len(raw))
	for i, c := range raw {
		cities[i] = c.city
	}
	counts, err := r.countNames(ctx, cities)
	if err != nil {
		return nil, err
	}

	out := make([]Candidate, 0, len(raw))
	for _, c := range raw {
		plain := stripDiacritics(c.city)
		display, full := r.naming(counts, c.city, plain, c.state, c.country, c.code)
		out = append(out, Candidate{
			Name:        plain,
			DisplayName: display,
			FullName:    full,
			DistKM:      c.distKM,
			hasMarks:    c.hasMarks,
		})
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

// PlaceMatch is one geonames entry matching a typed prefix, with coordinates
// so a caller needs no second lookup. The three names mean what they do on
// Candidate; ResolveByName resolves any of them.
type PlaceMatch struct {
	Name        string
	DisplayName string
	FullName    string
	Lat, Lon    float64
}

// SearchByName finds geonames entries starting with prefix, so a picker offers
// DB-backed names instead of free text that might resolve to nothing later.
func (r *Resolver) SearchByName(ctx context.Context, prefix string, limit int) ([]PlaceMatch, error) {
	// a typed name may already carry its qualifier — that is what the picker
	// offered and what an anchor is saved as, so it has to find the same row
	city, qualifiers := splitQualified(prefix)
	fetch := limit * searchOverfetchFactor
	rows, err := r.db.QueryContext(ctx,
		`SELECT gc.city, gc.latitude, gc.longitude,
		        COALESCE(gc.state, ''), COALESCE(gc.country, ''), COALESCE(gc.country_code, '')
		 FROM geonames_cities gc
		 WHERE gc.city LIKE ? || '%' COLLATE NOCASE
		 ORDER BY gc.city LIMIT ?`,
		city, fetch)
	if err != nil {
		return nil, fmt.Errorf("locationResolver: search: %w", err)
	}
	defer rows.Close()

	// same deferral as Candidates: filter and cut to limit on the row data
	// alone, then count names once for the survivors
	type row struct {
		city, plain, state, country, code string
		lat, lon                          float64
	}
	var raw []row
	seen := map[string]bool{}
	for rows.Next() {
		var name, state, country, code string
		var lat, lon float64
		if err := rows.Scan(&name, &lat, &lon, &state, &country, &code); err != nil {
			return nil, fmt.Errorf("locationResolver: scan: %w", err)
		}
		if !matchesQualifiers(state, country, qualifiers) {
			continue
		}
		plain := stripDiacritics(name)
		// the geonames database has near-duplicates a few hundred metres apart; listing
		// one string twice isn't a choice. ORDER BY makes "the first" stable.
		if full := fullName(plain, state, country); seen[full] {
			continue
		} else {
			seen[full] = true
		}
		raw = append(raw, row{city: name, plain: plain, state: state, country: country, code: code, lat: lat, lon: lon})
		if len(raw) == limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	cities := make([]string, len(raw))
	for i, c := range raw {
		cities[i] = c.city
	}
	counts, err := r.countNames(ctx, cities)
	if err != nil {
		return nil, err
	}

	out := make([]PlaceMatch, 0, len(raw))
	for _, c := range raw {
		display, full := r.naming(counts, c.city, c.plain, c.state, c.country, c.code)
		out = append(out, PlaceMatch{
			Name:        c.plain,
			DisplayName: display,
			FullName:    full,
			Lat:         c.lat, Lon: c.lon,
		})
	}
	return out, nil
}

// cityClaimed reports whether any anchor already takes this bare city name —
// the anchor gets the unqualified form, so geonames results must qualify.
func (r *Resolver) cityClaimed(city string) bool {
	for _, a := range r.Anchors {
		c, _, _ := strings.Cut(a.Name, ",")
		if strings.EqualFold(c, city) {
			return true
		}
	}
	return false
}

// disambiguate returns the smallest qualifier telling same-named cities apart.
// When unique and no anchor claims the bare name, returns just the city.
// On collision, appends the smallest distinguishing qualifier (state, then
// country). sep is " - " for folder names, ", " for display.
//
// Hyderabad alone → "Hyderabad"
// Hyderabad, India + Hyderabad, Pakistan with different states
//
//	→ "Hyderabad - Telangana", "Hyderabad - Sindh" (sep=" - ")
//	→ "Hyderabad, Telangana", "Hyderabad, Sindh" (sep=", ")
func disambiguate(city, state, country string, nameCount, countryCount, inCountryCount int, anchorClaims bool, sep string) string {
	if nameCount <= 1 && !anchorClaims {
		return city
	}
	// state even when the name also occurs abroad — no other Springfield in
	// Illinois exists to collide with, and a state is what a reader recognizes
	if inCountryCount > 1 && state != "" {
		return city + sep + stripDiacritics(state)
	}
	if countryCount > 1 && country != "" {
		return city + sep + stripDiacritics(country)
	}
	if state != "" {
		return city + sep + stripDiacritics(state)
	}
	return city
}

// fullName spells a place out for a person choosing from a list: city, state
// and country, skipping the parts the geonames database doesn't have. Two same-named
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

// stripDiacritics normalizes a geonames name ("Banjār") to plain ASCII
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

// Anchor is a saved place resolved to GPS coordinates.
type Anchor struct {
	Name       string // "Hyderabad, Telangana, India" (full, for ResolveByName)
	FolderName string // "Hyderabad" or "Hyderabad - India" when another anchor shares the city
	Lat        float64
	Lon        float64
}

// BuildAnchors resolves saved-place names and stores them in r.Anchors.
// Names that fail to resolve are skipped with a warning. FolderName is
// disambiguated when two anchors share the same city: the smallest qualifier
// that tells them apart (state, then country) is appended.
// It replaces any previous set rather than appending to it, so calling it twice
// in one process (a second scan, a rebuilt proposal) doesn't leave every anchor
// duplicated. Call it before a vfs run and never during one: Candidates reads
// r.Anchors through cityClaimed, and vfs resolves locations concurrently.
func (r *Resolver) BuildAnchors(ctx context.Context, savedPlaces []string) {
	if r == nil {
		return
	}
	r.Anchors = nil
	for _, name := range savedPlaces {
		lat, lon, err := r.ResolveByName(ctx, name)
		if err != nil {
			r.log.Warn("Could not resolve saved anchor town", "town", name, "error", err)
			continue
		}
		r.Anchors = append(r.Anchors, Anchor{Name: name, Lat: lat, Lon: lon})
	}
	// disambiguate FolderName using the same logic as disambiguate():
	// group anchors by bare city, compute collision counts per anchor, then
	// qualify with state or country only when needed.
	type entry struct{ city, state, country string }
	entries := make([]entry, len(r.Anchors))
	for i, a := range r.Anchors {
		city, qualifiers := splitQualified(a.Name)
		var state, country string
		if len(qualifiers) > 0 {
			state = qualifiers[0]
		}
		if len(qualifiers) > 1 {
			country = qualifiers[1]
		}
		entries[i] = entry{city, state, country}
	}
	// per-city: total count, distinct countries, count per country
	cityCount := map[string]int{}
	cityCountries := map[string]map[string]int{}
	for _, e := range entries {
		cityCount[e.city]++
		if cityCountries[e.city] == nil {
			cityCountries[e.city] = map[string]int{}
		}
		cityCountries[e.city][e.country]++
	}
	for i := range r.Anchors {
		e := entries[i]
		nameCnt := cityCount[e.city]
		countryCnt := len(cityCountries[e.city])
		inCountryCnt := cityCountries[e.city][e.country]
		r.Anchors[i].FolderName = disambiguate(e.city, e.state, e.country, nameCnt, countryCnt, inCountryCnt, false, " - ")
	}
}
