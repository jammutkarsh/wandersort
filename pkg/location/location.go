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
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/utils"
	_ "modernc.org/sqlite"
)

var ErrNoLocation = errors.New("locationResolver: location not found")

// maxDistSquared is the rejection threshold for the nearest-neighbour search,
// expressed as squared Euclidean distance in degree-space
// sqrt(0.01) = 0.1° ≈ 11 km
const maxDistSquared = 0.01

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
// closest match exceeds maxDistSquared
func (r *Resolver) queryNearest(ctx context.Context, lat, lon float64) (string, error) {
	// deltaDegrees lists the bounding-box half-widths to try in order
	//   0.09° ≈ 10 km — tight first pass, covers most intra-city lookups
	//   0.45° ≈ 50 km — wider fallback for rural or coastal photos
	// TODO: Consider using a range of values here instead of fixed deltas,
	// to better handle varying location densities across different regions
	deltaDegrees := []float64{0.09, 0.45}

	const query = `
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
		LIMIT  1`
	// TODO: Return all candidate locations ranked by distance instead of just the
	// nearest one. This will be useful in the VFS stage when the user wants to
	// correct a wrong location — we can suggest ranked options based on our findings
	// Once the user submits a value, search it again in the DB and store it so all
	// subsequent operations use the same location
	// Example: A user visits a niche place and uploads photos (session X). Later,
	// their friend uploads photos from the same place (session Y). Ideally both
	// sessions resolve to the same location

	for _, delta := range deltaDegrees {
		row := r.db.QueryRowContext(ctx, query, lat, lon, delta)

		var city string
		var dist float64
		if err := row.Scan(&city, &dist); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return "", fmt.Errorf("locationResolver: query: %w", err)
		}

		if dist > maxDistSquared {
			continue
		}

		return city, nil
	}

	return "", ErrNoLocation
}
