package migrations

import (
	"fmt"
	"slices"
	"time"

	"github.com/jmoiron/sqlx"
)

// Migration describes a single schema migration step
type Migration struct {
	Version     uint
	Description string
	SQL         []string
}

// schemas is the ordered list of migrations. Append new migrations at the end;
// never reorder or mutate existing entries
var schemas = []Migration{schema001, schema002, schema003}

// Run applies, in version order, any migrations not yet recorded in the
// database. Each version is tracked individually, so a lower-numbered
// migration added after a higher one has run is still applied. It creates the
// schema_migrations tracking table on first run
func Run(db *sqlx.DB) (int, error) {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			run_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z','now'))
		)
	`); err != nil {
		return 0, fmt.Errorf("error creating schema_migrations table: %w", err)
	}

	var versions []uint
	if err := db.Select(&versions, `SELECT version FROM schema_migrations`); err != nil {
		return 0, fmt.Errorf("error reading applied migration versions: %w", err)
	}
	applied := make(map[uint]bool, len(versions))
	for _, v := range versions {
		applied[v] = true
	}

	ordered := slices.Clone(schemas)
	slices.SortFunc(ordered, func(a, b Migration) int { return int(a.Version) - int(b.Version) })
	for i := 1; i < len(ordered); i++ {
		if ordered[i].Version == ordered[i-1].Version {
			return 0, fmt.Errorf("duplicate migration version %d", ordered[i].Version)
		}
	}

	ran := 0
	for _, schema := range ordered {
		if applied[schema.Version] {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return 0, fmt.Errorf("migration v%d: error beginning transaction: %w", schema.Version, err)
		}

		for _, stmt := range schema.SQL {
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				return 0, fmt.Errorf("migration v%d (%s): error executing SQL: %w", schema.Version, schema.Description, err)
			}
		}

		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, run_at) VALUES (?, ?)`,
			schema.Version, time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("migration v%d: error recording version: %w", schema.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("migration v%d: error committing transaction: %w", schema.Version, err)
		}
		ran++
	}

	if _, err := db.Exec("PRAGMA optimize"); err != nil {
		return 0, fmt.Errorf("error optimizing database: %w", err)
	}

	return ran, nil
}
