package locationdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync"

	_ "modernc.org/sqlite"
)

var (
	ErrNoLocation    = errors.New("locationdb: location not found")
	ErrNotConfigured = errors.New("locationdb: database path not configured")
)

type cacheKey struct {
	lat, lon float64
}

type LocationDB struct {
	db    *sql.DB
	mu    sync.Mutex
	cache map[cacheKey]string
}

func Open(dbPath string) (*LocationDB, error) {
	if dbPath == "" {
		return nil, ErrNotConfigured
	}

	dsn := fmt.Sprintf("file:%s?mode=ro&_journal=OFF&_sync=OFF", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("locationdb: open %q: %w", dbPath, err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("locationdb: ping %q: %w", dbPath, err)
	}

	return &LocationDB{
		db:    db,
		cache: make(map[cacheKey]string),
	}, nil
}

func (l *LocationDB) Lookup(ctx context.Context, lat, lon float64) (string, error) {
	key := cacheKey{
		lat: math.Round(lat*10000) / 10000,
		lon: math.Round(lon*10000) / 10000,
	}

	l.mu.Lock()
	if city, ok := l.cache[key]; ok {
		l.mu.Unlock()
		return city, nil
	}
	l.mu.Unlock()

	city, err := l.queryNearest(ctx, key.lat, key.lon)
	if err != nil {
		return "", err
	}

	l.mu.Lock()
	l.cache[key] = city
	l.mu.Unlock()

	return city, nil
}

// we use narrow bounding box first (±1°, ~111 km) to keep small, then rank by Euclidean distance
// in degree-space (fast, accurate enough at this scale), and pick the closest row.
// If the bounding box returns nothing, widen to ±5° before giving up.
func (l *LocationDB) queryNearest(ctx context.Context, lat, lon float64) (string, error) {
	const query = `
			SELECT name,
			       (latitude  - ?) * (latitude  - ?) +
			       (longitude - ?) * (longitude - ?) AS dist2
			FROM   locations
			WHERE  latitude  BETWEEN ? AND ?
			AND    longitude BETWEEN ? AND ?
			ORDER  BY dist2
			LIMIT  1`

	for _, delta := range []float64{1.0, 5.0} {
		row := l.db.QueryRowContext(ctx, query,
			lat, lat, lon, lon,
			lat-delta, lat+delta,
			lon-delta, lon+delta,
		)

		var name string
		var dist2 float64
		err := row.Scan(&name, &dist2)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return "", fmt.Errorf("locationdb: query: %w", err)
		}

		if dist2 > 2500 { // nearest location must within 50 coordinate units away
			return "", ErrNoLocation
		}

		return name, nil
	}

	return "", ErrNoLocation
}
