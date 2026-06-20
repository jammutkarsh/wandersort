package locationdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	_ "modernc.org/sqlite"
)

var ErrNoLocation = errors.New("locationdb: location not found")

// maxDistSquared is the rejection threshold for the nearest-neighbour search,
// expressed as squared Euclidean distance in degree-space.
// sqrt(0.01) = 0.1° ≈ 11 km.
const maxDistSquared = 0.01

type cacheKey struct {
	lat, lon float64
}

type DB struct {
	db    *db.DB
	cache sync.Map // No need for sync.Mutex or RWMutex
	sf    singleflight.Group
	log   logger.Logger
}

type locationMeta struct {
	Hash      string    `json:"sha256"`
	Version   string    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

func New(conn *db.DB, log logger.Logger) *DB {
	var count int
	if err := conn.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM geonames_cities`,
	).Scan(&count); err != nil {
		log.Error(fmt.Sprintf("verifying location database: %v", err))
		return nil
	}

	if count == 0 {
		log.Error(fmt.Sprintf("geonames_cities has no data"))
		return nil
	}

	return &DB{
		db:  conn,
		log: log,
	}
}

// Lookup returns the name of the nearest populated place for the given
// decimal-degree coordinates.
func (l *DB) Lookup(ctx context.Context, lat, lon float64) (string, error) {
	// Round to 4 decimal places ≈ 11 m precision — close enough that photos
	// from the same physical spot.
	// Formula: round(x * 10^4) / 10^4  keeps values stable across minor GPS jitter.
	key := cacheKey{
		lat: math.Round(lat*10000) / 10000,
		lon: math.Round(lon*10000) / 10000,
	}

	// 1. Load: Returns value and a boolean 'ok'
	if val, ok := l.cache.Load(key); ok {
		return val.(string), nil
	}

	// 2. Singleflight: Protect against cache stampede
	val, err, _ := l.sf.Do(fmt.Sprintf("%f:%f", key.lat, key.lon), func() (interface{}, error) {
		// Re-check cache in case another goroutine just finished the DB call
		if val, ok := l.cache.Load(key); ok {
			return val, nil
		}

		city, err := l.queryNearest(ctx, key.lat, key.lon)
		if err != nil {
			return "", err
		}

		// 3. Store: Cache the result
		l.cache.Store(key, city)
		return city, nil
	})

	if err != nil {
		return "", err
	}
	return val.(string), nil
}

// queryNearest finds the closest city name to the given coordinates.
//
// It queries using an expanding bounding box, then ranks candidates by squared
// Euclidean distance in degree-space, and returns the nearest match.
//
// Approximate real-world search radii:
//
//	Start: ~10 km  (±0.09°)
//	End:   ~50 km  (±0.45°)
//
// Returns ErrNoLocation if no candidate is found within either box, or if the
// closest match exceeds maxDistSquared.
func (l *DB) queryNearest(ctx context.Context, lat, lon float64) (string, error) {
	// deltaDegrees lists the bounding-box half-widths to try in order.
	//   0.09° ≈ 10 km — tight first pass, covers most intra-city lookups.
	//   0.45° ≈ 50 km — wider fallback for rural or coastal photos.
	// TODO: Consider using a range of values here instead of fixed deltas,
	// to better handle varying location densities across different regions.
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
	// correct a wrong location — we can suggest ranked options based on our findings.
	// Once the user submits a value, search it again in the DB and store it so all
	// subsequent operations use the same location.
	// Example: A user visits a niche place and uploads photos (session X). Later,
	// their friend uploads photos from the same place (session Y). Ideally both
	// sessions resolve to the same location.

	for _, delta := range deltaDegrees {
		row := l.db.QueryRowContext(ctx, query, lat, lon, delta)

		var city string
		var dist float64
		if err := row.Scan(&city, &dist); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return "", fmt.Errorf("locationdb: query: %w", err)
		}

		if dist > maxDistSquared {
			continue
		}

		return city, nil
	}

	return "", ErrNoLocation
}
