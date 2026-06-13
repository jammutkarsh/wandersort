package locationdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/logger"
	_ "modernc.org/sqlite"
)

var (
	ErrNoLocation    = errors.New("locationdb: location not found")
	ErrNotConfigured = errors.New("locationdb: database path not configured")
)

const (
	// downloadBaseURL is the download URL for the locationDB asset.
	// The DB file is updated on the 1st of every month; location.json holds
	// the metadata (version, date) so we can decide whether to re-download.
	downloadBaseURL = "https://locationdb.utkarshchourasia.in"

	// dbFileName is the SQLite database file name inside $HOME/.wandersort/
	dbFileName = "location.db"

	// metaFileName is the metadata JSON file published alongside the DB.
	metaFileName = "location.json"

	// maxDistSquared is the rejection threshold for the nearest-neighbour search,
	// expressed as squared Euclidean distance in degree-space.
	//
	// sqrt(0.01) = 0.1° ≈ 11 km at the equator. Any candidate farther than that
	// is treated as "no location found" and ErrNoLocation is returned instead.
	maxDistSquared = 0.01
)

// cacheKey is the lookup key for the in-memory result cache.
// Coordinates are rounded to 4 decimal places (≈11 m precision) before use,
// so burst photos taken at the same spot share a single DB round-trip.
type cacheKey struct {
	lat, lon float64
}

// LocationDB holds a read-only connection to the GeoNames SQLite database and
// an in-memory result cache.
type DB struct {
	db    *sql.DB
	mu    sync.Mutex
	cache map[cacheKey]string
	log   logger.Logger
}

// locationMeta mirrors the JSON structure of location.json in the release.
type locationMeta struct {
	Version   string    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// New downloads the DB file if it is absent and initializes a new DB instance.
func New(dbPath string, log logger.Logger) (*DB, error) {
	if err := ensureDB(dbPath, log); err != nil {
		return nil, fmt.Errorf("locationdb: ensure db: %w", err)
	}
	return open(dbPath, log)
}

// ensureDB downloads location.db into the parent directory of dbPath if the file does not already exist.
// The parent directory is created with 0755 permissions if needed.
func ensureDB(dbPath string, log logger.Logger) error {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create dir %q: %w", dir, err)
	}

	if _, err := os.Stat(dbPath); err == nil {
		log.Info("locationdb found; no need to download", "database filename", dbFileName)
		return nil
	}

	log.Info("locationdb not found; downloading from", "download URL", downloadBaseURL+"/"+dbFileName)
	if err := downloadFile(dbPath, downloadBaseURL+"/"+dbFileName); err != nil {
		return fmt.Errorf("download %s: %w", dbFileName, err)
	}

	// Also download the metadata file next to the DB so the user (and future
	// tooling) can inspect the version without opening the SQLite file.
	metaPath := filepath.Join(dir, metaFileName)
	if err := downloadFile(metaPath, downloadBaseURL+"/"+metaFileName); err != nil {
		// Non-fatal: the DB itself is what matters.
		log.Info("locationdb: warning: could not download metadata", "file", metaFileName, "error", err)
	}

	return nil
}

// downloadFile fetches url and writes the body to dest atomically (via a temp
// file) so a partial download never leaves a corrupt file at dest.
func downloadFile(dest, url string) error {
	resp, err := http.Get(url) //nolint:noctx // startup path, no context available
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}

	// Write to a temp file in the same directory so os.Rename is atomic.
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".dl-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op if Rename succeeded
	}()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("rename to %s: %w", dest, err)
	}

	return nil
}

// ReadMeta reads and returns the metadata stored in location.json next to the
// DB file. Useful for logging or displaying the DB version to the user.
func ReadMeta(dbPath string) (*locationMeta, error) {
	metaPath := filepath.Join(filepath.Dir(dbPath), metaFileName)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("locationdb: read meta: %w", err)
	}
	var m locationMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("locationdb: parse meta: %w", err)
	}
	return &m, nil
}

// open opens the GeoNames SQLite database at dbPath in read-only mode.
// Returns ErrNotConfigured if dbPath is empty.
func open(dbPath string, log logger.Logger) (*DB, error) {
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

	log.Info("Successfully connected to location database", "path", dbPath)
	return &DB{
		db:    db,
		cache: make(map[cacheKey]string),
		log:   log,
	}, nil
}

// Close closes the underlying SQLite database connection.
// Should be called during the application is shutting down.
func (l *DB) Close() error {
	return l.db.Close()
}

// Lookup returns the name of the nearest populated place for the given
// decimal-degree coordinates.
//
// Results are cached: coordinates rounded to 4 decimal places (~11 m
// precision) share one cache entry, so photos taken at virtually the same
// spot never hit the database twice.
func (l *DB) Lookup(ctx context.Context, lat, lon float64) (string, error) {
	// Round to 4 decimal places ≈ 11 m precision — close enough that photos
	// from the same physical spot.
	// Formula: round(x * 10^4) / 10^4  keeps values stable across minor GPS jitter.
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
	deltaDegrees := []float64{0.09, 0.45}

	// CTE passes lat, lon, and delta as named params so each appears only once,
	// avoiding the error-prone repetition of positional ? placeholders.
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
			return "", ErrNoLocation
		}

		return city, nil
	}

	return "", ErrNoLocation
}
