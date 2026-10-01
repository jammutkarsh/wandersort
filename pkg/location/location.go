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

// gpsRoundingFactor rounds coordinates to 2 decimals (≈ 1.1 km) for Lookup's
// cache key only; the query uses the real coordinates.
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
	cache *sync.Map // Lookup results; shared by WithAnchors views
	log   logger.Logger
	// anchors make Candidates/SearchByName qualify a name a saved place
	// already claims. Set only through WithAnchors.
	anchors []Anchor
}

// NewResolver wraps an already-open, verified location database (pkg/install
// downloads and verifies it).
func NewResolver(locationDB *db.DB, log logger.Logger) *Resolver {
	return &Resolver{db: locationDB, cache: &sync.Map{}, log: log}
}

// WithAnchors returns a view of r whose name lists qualify the bare city
// names these anchors claim. r itself is unchanged.
func (r *Resolver) WithAnchors(anchors []Anchor) *Resolver {
	if r == nil {
		return nil
	}
	view := *r
	view.anchors = anchors
	return &view
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

	// query the real coordinates; the grid only shares cache entries
	city, err := r.queryNearest(ctx, lat, lon)
	switch {
	// cache "nothing near" too, but only for ErrNoLocation: a cancelled or
	// failed query must stay retryable
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
		// the review picker and saved anchors are, so same-named cities stay apart
		return cands[0].DisplayName, nil
	}
	return "", ErrNoLocation
}

// Candidate is one ranked reverse-geocode match. Folders named automatically
// use DisplayName; pick lists show FullName and write FolderName.
type Candidate struct {
	Name        string // plain city, no qualifier: "<city>"
	DisplayName string // smallest unique qualifier: "<city>, <state>"
	FullName    string // spelled out, what a picker shows: "<city>, <state>, <country>"
	FolderName  string // DisplayName, sanitized — what a picker writes if this is chosen
	DistKM      float64
	hasMarks    bool // the geonames entry carried diacritics stripDiacritics removed
}

// candidateFetchLimit over-fetches so Candidates can prefer a plain-spelled
// entry over a diacritic one at roughly the same distance.
const candidateFetchLimit = 32

// searchOverfetchFactor over-fetches in SearchByName, whose qualifier and
// dedup filters run after the query.
const searchOverfetchFactor = 8

// minFuzzyPrefix is the shortest typed prefix the fuzzy fallback runs for —
// below this, a trigram or 2-char scan is too broad to rank meaningfully.
const minFuzzyPrefix = 2

// fuzzyFetchLimit bounds both fuzzy fallbacks' broad scan so a short prefix
// doesn't pull tens of thousands of rows into Go for ranking.
const fuzzyFetchLimit = 500

// maxLevenshteinDist is the fuzzy fallback's max edit distance: catches one- or
// two-letter typos, excludes a different city four edits away.
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

// countNames answers those three questions for a few city names in one indexed
// GROUP BY, asked only for the rows a caller gets back.
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
		// only a real country code counts as a country, like
		// COUNT(DISTINCT country_code) ignoring NULLs
		counts.total += n
		if code != "" {
			counts.countries++
			counts.inCountry[code] += n
		}
		out[nocaseKey(city)] = counts
	}
	return out, rows.Err()
}

// nocaseKey folds ASCII letters only, like SQLite's NOCASE, so it groups the
// way the GROUP BY did.
func nocaseKey(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

// geoRow is one scanned geonames row; names needing counts are filled in after
// ranking and limiting.
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
// plain spellings ahead of diacritic ones.
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

// ResolveByName forward-geocodes a saved place name: exact case-insensitive
// match first, then a diacritic-stripped pass.
func (r *Resolver) ResolveByName(ctx context.Context, name string) (lat, lon float64, err error) {
	// honour the qualifiers: the bare city alone would resolve to whichever
	// same-named row comes back first
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

// PlaceMatch is one geonames entry matching a typed prefix, with coordinates.
// Names mean what they do on Candidate.
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
	// before giving up, so a one-letter typo still finds the city.
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

// fuzzySearch is SearchByName's typo-tolerant fallback: a trigram match, or a
// Levenshtein scan when the database has no trigram table.
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

// fuzzySearchTrigram narrows to rows sharing a trigram with city, then ranks by
// edit distance: trigram overlap is recall, the distance cutoff decides.
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

// fuzzySearchLevenshtein ranks every row sharing city's first 2 chars by edit
// distance. Slower; for databases without geonames_trigrams.
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

// scanFuzzyRows reads both fuzzy queries' rows with the exact query's qualifier
// filter and dedup. limit 0 means unbounded.
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

// trigrams slides a 3-char window over city (lowercased, stripped, padded with a
// space each side), matching how geonames_trigrams is built.
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

// canonicalSearchLimit is how many rows Canonical scans for an exact match.
const canonicalSearchLimit = 8

// ErrNotExact means a typed place name only nearly matches a geonames entry.
var ErrNotExact = errors.New("no exact match")

// Canonical returns the spelling a typed place name must be stored as: geonames'
// own form, qualified enough to resolve back to one row. An unknown name comes
// back unchanged; a near-miss is ErrNotExact naming the closest alternative; a
// database failure is returned as is.
func (r *Resolver) Canonical(ctx context.Context, typed string) (string, error) {
	typed = strings.TrimSpace(typed)
	if typed == "" || r == nil {
		return "", fmt.Errorf("locationResolver: no place name given")
	}
	matches, err := r.SearchByName(ctx, typed, canonicalSearchLimit)
	if err != nil {
		return "", fmt.Errorf("look up %q: %w", typed, err)
	}
	if len(matches) == 0 {
		return typed, nil
	}
	if name, ok := exactMatch(matches, typed); ok {
		return name, nil
	}
	return "", fmt.Errorf("%w for %q (did you mean %s?)", ErrNotExact, typed, matches[0].FullName)
}

// SuggestNames lists FullName completions for a prefix: the qualified form is
// what Canonical saves and ResolveByName round-trips.
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

// exactMatch returns geonames' spelling when a match equals typed (case-
// insensitive). Qualified forms first, so a bare name is still stored
// qualified and can't resolve to a same-named city elsewhere.
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

// disambiguate returns city plus the smallest qualifier that tells same-named
// cities apart (state, then country), or just city when unique and no anchor
// claims it. sep is " - " for folder names, ", " for display.
func disambiguate(city, state, country string, nameCount, countryCount, inCountryCount int, anchorClaims bool, sep string) string {
	if nameCount <= 1 && !anchorClaims {
		return city
	}
	// state even when the name also occurs abroad: nothing in that state
	// collides, and a state is what a reader recognizes
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

// fullName spells a place out (city, state, country, skipping missing parts)
// for pick lists.
func fullName(city, state, country string) string {
	parts := []string{city}
	for _, p := range []string{state, country} {
		if p = stripDiacritics(strings.TrimSpace(p)); p != "" && p != parts[len(parts)-1] {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ", ")
}

// splitQualified splits "City, State, Country" into city and qualifiers.
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

// matchesQualifiers reports whether state/country account for every qualifier.
// No qualifiers matches anything.
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

// stripDiacritics removes combining marks from a geonames name ("ā" → "a"):
// they read as typos in an unfamiliar script.
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
	Name       string // "<city>, <state>, <country>" (full, for ResolveByName)
	FolderName string // "<city>", or "<city> - <state>" when another anchor shares the city
	Lat        float64
	Lon        float64
}

// BuildAnchors resolves saved-place names to coordinates, skipping (with a
// warning) names that don't resolve. Two anchors sharing a city get the
// smallest qualifier that tells them apart in FolderName.
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
	// qualify FolderName only where anchors share a city, as disambiguate does
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
	return anchors
}
