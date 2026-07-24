// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package location

import (
	"context"
	"database/sql"
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
	// resolver (setup, scan, serve, review), so tagging it printed a checksum
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
		return cands[0].Name, nil
	}
	return "", ErrNoLocation
}

// Candidate is one ranked reverse-geocode match
type Candidate struct {
	Name     string
	DistKM   float64
	hasMarks bool // the raw gazetteer entry carried diacritics stripDiacritics removed
}

// candidateFetchLimit over-fetches so Candidates has enough rows to prefer a
// plain-spelled entry over a diacritic one at roughly the same location,
// rather than just returning whichever the distance sort happened to put first.
const candidateFetchLimit = 32

// candidateQuery is shared by Candidates and queryNearest
const candidateQuery = `
	WITH params AS (
    SELECT ? AS lat, ? AS lon, ? AS delta
	)
	SELECT gc.city,
    	(gc.latitude  - p.lat) * (gc.latitude  - p.lat) +
    	(gc.longitude - p.lon) * (gc.longitude - p.lon) AS dist
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
		var city string
		var distSq float64
		if err := rows.Scan(&city, &distSq); err != nil {
			return nil, fmt.Errorf("locationResolver: scan: %w", err)
		}
		if distSq > MaxDistSquared {
			continue
		}
		plain := stripDiacritics(city)
		out = append(out, Candidate{Name: plain, DistKM: math.Sqrt(distSq) * kmPerDegree, hasMarks: plain != city})
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
	err = r.db.QueryRowContext(ctx,
		`SELECT latitude, longitude FROM geonames_cities WHERE city = ? COLLATE NOCASE LIMIT 1`,
		name).Scan(&lat, &lon)
	if err == nil {
		return lat, lon, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, fmt.Errorf("locationResolver: resolve by name: %w", err)
	}
	return r.resolveStripped(ctx, name)
}

// resolveStripped is ResolveByName's fallback for names whose gazetteer entry
// carries diacritics. SQLite's NOCASE collation is ASCII-only and there's no
// stripped column to index, so this scans.
// ponytail: full table scan, run at most twice per scan (home + work anchor)
// and only when the exact match missed. Add a normalized column + index if it
// ever lands on a hot path.
func (r *Resolver) resolveStripped(ctx context.Context, name string) (lat, lon float64, err error) {
	want := strings.ToLower(stripDiacritics(name))
	rows, err := r.db.QueryContext(ctx, `SELECT city, latitude, longitude FROM geonames_cities`)
	if err != nil {
		return 0, 0, fmt.Errorf("locationResolver: resolve by name: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var city string
		var clat, clon float64
		if err := rows.Scan(&city, &clat, &clon); err != nil {
			return 0, 0, fmt.Errorf("locationResolver: scan: %w", err)
		}
		if strings.ToLower(stripDiacritics(city)) == want {
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
type PlaceMatch struct {
	Name     string
	Lat, Lon float64
}

// SearchByName finds gazetteer entries whose name starts with prefix, so an
// interactive picker (e.g. the setup anchor prompt) can offer exact DB-backed
// names instead of free text that might not match anything — the picked name
// can never drift from what's actually in the location DB.
func (r *Resolver) SearchByName(ctx context.Context, prefix string, limit int) ([]PlaceMatch, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT city, latitude, longitude FROM geonames_cities
		 WHERE city LIKE ? || '%' COLLATE NOCASE ORDER BY city LIMIT ?`,
		prefix, limit)
	if err != nil {
		return nil, fmt.Errorf("locationResolver: search: %w", err)
	}
	defer rows.Close()

	var out []PlaceMatch
	for rows.Next() {
		var name string
		var lat, lon float64
		if err := rows.Scan(&name, &lat, &lon); err != nil {
			return nil, fmt.Errorf("locationResolver: scan: %w", err)
		}
		out = append(out, PlaceMatch{Name: stripDiacritics(name), Lat: lat, Lon: lon})
	}
	return out, rows.Err()
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
