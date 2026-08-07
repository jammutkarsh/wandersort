// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package location

import (
	"context"
	"database/sql"
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
	"github.com/jammutkarsh/wandersort/pkg/path"
	_ "modernc.org/sqlite"
)

var ErrNoLocation = errors.New("locationResolver: location not found")

// MaxDistSquared rejects a match beyond ~50km (squared degree-space distance).
// Exported: vfs.resolveLocations reuses it for the anchor-fold radius.
const MaxDistSquared = 0.2025

// gpsRoundingFactor rounds coordinates to 2 decimal places (≈ 1.1 km) for the
// Lookup cache key only — the query itself runs on the real coordinates, so a
// coarse grid costs no accuracy. It decides how often a walk around town pays
// for a fresh query: an 11 m grid billed one every eleven metres.
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
	// anchors are saved places resolved to GPS coordinates, kept only so
	// cityClaimed can qualify a candidate name against them. Nil until
	// BuildAnchors is called, which is also what hands them to callers.
	anchors []Anchor
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

	// query the real coordinates, not the rounded ones: the grid exists to share
	// a cache entry, and naming the square's centre would move the query point
	// up to half a cell away from where the photo was taken
	city, err := r.queryNearest(ctx, lat, lon)
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
		// a blank name is no more usable as a folder than no match at all, and
		// Lookup's cache uses "" as its miss sentinel
		if len(cands) == 0 || cands[0].DisplayName == "" {
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
// picks from shows FullName as the label (FullName doubles as that
// suggestion name) and writes FolderName if picked — this package's job, not
// a caller's: it already knows which qualifier a name needs, so it also
// knows what's safe to put in a directory name.
type Candidate struct {
	Name        string // plain city, no qualifier: "Springfield"
	DisplayName string // smallest unique qualifier: "Springfield, Illinois"
	FullName    string // spelled out, and what a picker shows as the suggestion: "Springfield, Illinois, United States"
	FolderName  string // DisplayName, sanitized — what a picker writes if this is chosen
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

// minFuzzyPrefix is the shortest typed prefix the fuzzy fallback runs for —
// below this, a trigram or 2-char scan is too broad to rank meaningfully.
const minFuzzyPrefix = 2

// fuzzyFetchLimit bounds both fuzzy fallbacks' broad scan so a short prefix
// doesn't pull tens of thousands of rows into Go for ranking.
const fuzzyFetchLimit = 500

// maxLevenshteinDist is the Levenshtein fallback's max edit distance for a
// typo-tolerant match: ≤2 covers "katmandu"→"Kathmandu" (1) and
// "kathmendo"→"Kathmandu" (2), while "mumbai"→"Mombasa" (4) is correctly
// excluded.
const maxLevenshteinDist = 2

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
	for _, city := range cities {
		if key := nocaseKey(city); !seen[key] {
			seen[key] = true
			want = append(want, city)
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
		counts := out[nocaseKey(city)]
		if counts.inCountry == nil {
			counts.inCountry = map[string]int{}
		}
		// every row carrying the name counts toward the total, but only a real
		// country code is a country — matching COUNT(DISTINCT country_code),
		// which ignores NULLs
		counts.total += n
		if code != "" {
			counts.countries++
			counts.inCountry[code] += n
		}
		out[nocaseKey(city)] = counts
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

// geoRow is one geonames row as both queries scan it, before the names that
// need library-wide counts are filled in. Ranking, filtering and limiting run
// on this alone, so countNames is only ever asked about the survivors.
type geoRow struct {
	city, state, country, code        string
	plain                             string // city with diacritics stripped
	displayName, fullName, folderName string // filled by fillNames
	lat, lon                          float64
	distKM                            float64
	hasMarks                          bool
}

// fillNames sets displayName, fullName and folderName on every row, counting
// each distinct city name once for the whole batch rather than per row.
func (r *Resolver) fillNames(ctx context.Context, rows []geoRow) error {
	cities := make([]string, len(rows))
	for i := range rows {
		cities[i] = rows[i].city
	}
	counts, err := r.countNames(ctx, cities)
	if err != nil {
		return err
	}
	for i := range rows {
		row := &rows[i]
		count := counts[nocaseKey(row.city)]
		row.displayName = disambiguate(row.plain, row.state, row.country,
			count.total, count.countries, count.inCountry[row.code], r.cityClaimed(row.plain), ", ")
		row.fullName = fullName(row.plain, row.state, row.country)
		row.folderName = path.SanitizeSegment(row.displayName)
	}
	return nil
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

	var raw []geoRow
	for rows.Next() {
		var city, state, country, code string
		var distSq float64
		if err := rows.Scan(&city, &distSq, &state, &country, &code); err != nil {
			return nil, fmt.Errorf("locationResolver: scan: %w", err)
		}
		if distSq > MaxDistSquared {
			continue
		}
		plain := stripDiacritics(city)
		raw = append(raw, geoRow{
			city: city, state: state, country: country, code: code, plain: plain,
			distKM:   math.Sqrt(distSq) * kmPerDegree,
			hasMarks: plain != city,
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
	if err := r.fillNames(ctx, raw); err != nil {
		return nil, err
	}

	out := make([]Candidate, 0, len(raw))
	for _, row := range raw {
		out = append(out, Candidate{
			Name:        row.plain,
			DisplayName: row.displayName,
			FullName:    row.fullName,
			FolderName:  row.folderName,
			DistKM:      row.distKM,
			hasMarks:    row.hasMarks,
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
// so a caller needs no second lookup. The names mean what they do on
// Candidate; ResolveByName resolves Name, DisplayName or FullName.
type PlaceMatch struct {
	Name        string
	DisplayName string
	FullName    string
	FolderName  string
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

	var raw []geoRow
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
		raw = append(raw, geoRow{
			city: name, state: state, country: country, code: code, plain: plain,
			lat: lat, lon: lon,
		})
		if len(raw) == limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The exact prefix query found nothing — try a typo-tolerant match
	// before giving up, so "katmandu" still finds "Kathmandu".
	if len(raw) == 0 && len(city) >= minFuzzyPrefix {
		fuzzy, err := r.fuzzySearch(ctx, city, qualifiers, limit)
		if err != nil {
			return nil, err
		}
		raw = fuzzy
	}

	if err := r.fillNames(ctx, raw); err != nil {
		return nil, err
	}

	out := make([]PlaceMatch, 0, len(raw))
	for _, row := range raw {
		out = append(out, PlaceMatch{
			Name:        row.plain,
			DisplayName: row.displayName,
			FullName:    row.fullName,
			FolderName:  row.folderName,
			Lat:         row.lat, Lon: row.lon,
		})
	}
	return out, nil
}

// fuzzySearch is SearchByName's typo-tolerant fallback: a trigram match
// against geonames_trigrams (the external DB's own pg_trgm-style index),
// falling back to a Go-side Levenshtein scan when that table doesn't exist
// yet — a location.db downloaded before the trigram index shipped.
func (r *Resolver) fuzzySearch(ctx context.Context, city string, qualifiers []string, limit int) ([]geoRow, error) {
	rows, err := r.fuzzySearchTrigram(ctx, city, qualifiers, limit)
	if err == nil {
		return rows, nil
	}
	if !strings.Contains(err.Error(), "no such table") {
		return nil, fmt.Errorf("locationResolver: fuzzy search: %w", err)
	}
	return r.fuzzySearchLevenshtein(ctx, city, qualifiers, limit)
}

// fuzzySearchTrigram narrows to geonames_trigrams matches sharing at least
// one 3-char window with city — the same idea pg_trgm uses to touch only
// rows that could plausibly match, instead of scanning the whole table —
// then ranks that pool by real edit distance (rankByDistance): trigram
// overlap alone is a recall signal, not a precision one (a long garbage
// string can share several trigrams with a short real city purely by
// coincidence), so the edit-distance cutoff is still what decides a match.
func (r *Resolver) fuzzySearchTrigram(ctx context.Context, city string, qualifiers []string, limit int) ([]geoRow, error) {
	grams := trigrams(city)
	if len(grams) == 0 {
		return nil, nil
	}
	args := make([]any, len(grams)+1)
	for i, g := range grams {
		args[i] = g
	}
	args[len(grams)] = fuzzyFetchLimit

	rows, err := r.db.QueryContext(ctx,
		`SELECT gc.city, gc.latitude, gc.longitude,
		        COALESCE(gc.state, ''), COALESCE(gc.country, ''), COALESCE(gc.country_code, '')
		 FROM geonames_trigrams gt
		 JOIN geonames_cities gc ON gt.city_id = gc.rowid
		 WHERE gt.trigram IN (?`+strings.Repeat(",?", len(grams)-1)+`)
		 GROUP BY gt.city_id
		 ORDER BY COUNT(*) DESC
		 LIMIT ?`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cands, err := scanFuzzyRows(rows, qualifiers, 0) // no limit yet: rankByDistance needs the whole pool
	if err != nil {
		return nil, err
	}
	return rankByDistance(cands, city, limit), nil
}

// fuzzySearchLevenshtein is the safety net from before the trigram index
// existed: widen to the first 2 chars of city, then rank every hit by edit
// distance. Broader and slower than the trigram query (no index to narrow
// the scan), but needs nothing from the database beyond geonames_cities
// itself, so it still works against a location.db downloaded before the
// trigram table shipped.
func (r *Resolver) fuzzySearchLevenshtein(ctx context.Context, city string, qualifiers []string, limit int) ([]geoRow, error) {
	fuzzyPrefix := city[:min(2, len(city))]
	rows, err := r.db.QueryContext(ctx,
		`SELECT gc.city, gc.latitude, gc.longitude,
		        COALESCE(gc.state, ''), COALESCE(gc.country, ''), COALESCE(gc.country_code, '')
		 FROM geonames_cities gc
		 WHERE gc.city LIKE ? || '%' COLLATE NOCASE
		 ORDER BY gc.city LIMIT ?`,
		fuzzyPrefix, fuzzyFetchLimit)
	if err != nil {
		return nil, fmt.Errorf("locationResolver: fuzzy search: %w", err)
	}
	defer rows.Close()

	cands, err := scanFuzzyRows(rows, qualifiers, 0) // no limit yet: rankByDistance needs the whole pool
	if err != nil {
		return nil, err
	}
	return rankByDistance(cands, city, limit), nil
}

// rankByDistance orders cands by Levenshtein distance to city and keeps only
// those within maxLevenshteinDist, capped at limit (0 = unbounded).
func rankByDistance(cands []geoRow, city string, limit int) []geoRow {
	want := strings.ToLower(city)
	sort.SliceStable(cands, func(i, j int) bool {
		return levenshtein(strings.ToLower(cands[i].plain), want) < levenshtein(strings.ToLower(cands[j].plain), want)
	})
	out := cands[:0]
	for _, c := range cands {
		if levenshtein(strings.ToLower(c.plain), want) > maxLevenshteinDist {
			continue
		}
		out = append(out, c)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

// scanFuzzyRows reads the common (city, lat, lon, state, country, code) shape
// both fuzzy queries select, applying the same qualifier filter and
// full-name dedup the exact-prefix query uses. limit == 0 means unbounded.
func scanFuzzyRows(rows *sql.Rows, qualifiers []string, limit int) ([]geoRow, error) {
	var out []geoRow
	seen := map[string]bool{}
	for rows.Next() {
		var name, state, country, code string
		var lat, lon float64
		if err := rows.Scan(&name, &lat, &lon, &state, &country, &code); err != nil {
			return nil, fmt.Errorf("locationResolver: scan fuzzy: %w", err)
		}
		if !matchesQualifiers(state, country, qualifiers) {
			continue
		}
		plain := stripDiacritics(name)
		if full := fullName(plain, state, country); seen[full] {
			continue
		} else {
			seen[full] = true
		}
		out = append(out, geoRow{city: name, state: state, country: country, code: code, plain: plain, lat: lat, lon: lon})
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, rows.Err()
}

// trigrams slides a 3-char window over city, lowercased/diacritic-stripped
// and padded with a space on each side so the first and last characters
// participate in at least one trigram — the same shape the external
// locationDB build indexes into geonames_trigrams.
func trigrams(city string) []string {
	r := []rune(" " + strings.ToLower(stripDiacritics(city)) + " ")
	if len(r) < 3 {
		return nil
	}
	out := make([]string, 0, len(r)-2)
	for i := 0; i+3 <= len(r); i++ {
		out = append(out, string(r[i:i+3]))
	}
	return out
}

// levenshtein returns the edit distance between a and b (Wagner-Fischer,
// two rows instead of a full matrix — O(len(a)*len(b)) time, O(min) space).
func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// canonicalSearchLimit is how many rows Canonical looks through for an exact
// match. Enough to reach past the near-duplicates a popular name attracts,
// small enough that a rejection can name one alternative.
const canonicalSearchLimit = 8

// Canonical returns the spelling a typed place name must be stored as: the
// geonames own form, qualified far enough to resolve back to exactly one row.
// It is the write side of ResolveByName — this package decides both, so a name
// saved by a picker is always one this package can find again.
//
// A name the database has never heard of comes back unchanged (a village
// geonames is missing must not be undismissable), but a near-miss is an error
// naming the closest alternative: silently saving "Hyderbad" would anchor the
// library to whatever that eventually resolved to.
func (r *Resolver) Canonical(ctx context.Context, typed string) (string, error) {
	typed = strings.TrimSpace(typed)
	if typed == "" || r == nil {
		return "", fmt.Errorf("locationResolver: no place name given")
	}
	matches, err := r.SearchByName(ctx, typed, canonicalSearchLimit)
	if err != nil || len(matches) == 0 {
		return typed, nil
	}
	if name, ok := exactMatch(matches, typed); ok {
		return name, nil
	}
	return "", fmt.Errorf("no exact match for %q (did you mean %s?)", typed, matches[0].FullName)
}

// SuggestNames lists place names for a picker to offer as prefix completions.
// FullName, not the bare city: six identical "Springfield"s are unpickable,
// and the qualified form is what Canonical saves and ResolveByName round-trips.
func (r *Resolver) SuggestNames(ctx context.Context, prefix string, limit int) []string {
	matches, err := r.SearchByName(ctx, prefix, limit)
	if err != nil {
		return nil
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.FullName
	}
	return names
}

// exactMatch returns geonames' own spelling when one of matches is a
// case-insensitive match for typed. The qualified forms are tried first: a
// bare "Hyderabad" must still be stored qualified, or it resolves back to
// whichever row the database happens to return first — which is how a home
// town in India became one in Pakistan.
func exactMatch(matches []PlaceMatch, typed string) (string, bool) {
	typed = strings.TrimSpace(typed)
	for _, m := range matches {
		if strings.EqualFold(m.FullName, typed) {
			return canonicalNameOf(m), true
		}
	}
	for _, m := range matches {
		if strings.EqualFold(m.DisplayName, typed) || strings.EqualFold(m.Name, typed) {
			return canonicalNameOf(m), true
		}
	}
	return "", false
}

// canonicalNameOf is the form a place is saved as: the fullest spelling the
// geonames row supports.
func canonicalNameOf(m PlaceMatch) string {
	switch {
	case m.FullName != "":
		return m.FullName
	case m.DisplayName != "":
		return m.DisplayName
	default:
		return m.Name
	}
}

// cityClaimed reports whether any anchor already takes this bare city name —
// the anchor gets the unqualified form, so geonames results must qualify.
func (r *Resolver) cityClaimed(city string) bool {
	for _, a := range r.anchors {
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

// BuildAnchors resolves saved-place names to coordinates and returns them.
// Names that fail to resolve are skipped with a warning. FolderName is
// disambiguated when two anchors share the same city: the smallest qualifier
// that tells them apart (state, then country) is appended.
//
// The set is also kept on the resolver, because Candidates needs it (via
// cityClaimed) to decide how a nearby place is spelled. Callers that place
// files take the returned slice rather than reading that field back — anchors
// are a value the caller passes on, not state two packages share.
//
// It replaces any previous set rather than appending to it, so calling it twice
// in one process (a second scan, a rebuilt proposal) doesn't leave every anchor
// duplicated. The whole set is built locally and published in one assignment, so
// a concurrent reader sees either the old anchors or the new ones, never an
// empty window mid-rebuild.
func (r *Resolver) BuildAnchors(ctx context.Context, savedPlaces []string) []Anchor {
	if r == nil {
		return nil
	}
	var anchors []Anchor
	for _, name := range savedPlaces {
		lat, lon, err := r.ResolveByName(ctx, name)
		if err != nil {
			r.log.Warn("Could not resolve saved anchor town", "town", name, "error", err)
			continue
		}
		anchors = append(anchors, Anchor{Name: name, Lat: lat, Lon: lon})
	}
	// disambiguate FolderName using the same logic as disambiguate():
	// group anchors by bare city, compute collision counts per anchor, then
	// qualify with state or country only when needed.
	type entry struct{ city, state, country string }
	entries := make([]entry, len(anchors))
	for i, a := range anchors {
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
	for i := range anchors {
		e := entries[i]
		nameCount := cityCount[e.city]
		countryCount := len(cityCountries[e.city])
		inCountryCount := cityCountries[e.city][e.country]
		anchors[i].FolderName = disambiguate(e.city, e.state, e.country, nameCount, countryCount, inCountryCount, false, " - ")
	}
	r.anchors = anchors
	return anchors
}
