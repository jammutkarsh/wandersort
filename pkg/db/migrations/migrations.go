package migrations

import (
	"database/sql"
	"fmt"
	"time"
)

// Migration describes a single schema migration step.
type Migration struct {
	Version     uint
	Description string
	SQL         string
}

// schemas is the ordered list of migrations. Append new migrations at the end;
// never reorder or mutate existing entries.
var schemas = []Migration{schema001, schema002}

// Run applies any migrations whose version is greater than what the database
// has already recorded. It creates the schema_migrations tracking table on
// first run.
func Run(db *sql.DB) (int, error) {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			run_at  TEXT NOT NULL DEFAULT (datetime('now'))
		)
	`); err != nil {
		return 0, fmt.Errorf("error creating schema_migrations table: %w", err)
	}

	var current uint
	row := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`)
	if err := row.Scan(&current); err != nil {
		return 0, fmt.Errorf("error reading current migration version: %w", err)
	}

	for _, schema := range schemas {
		if schema.Version <= current {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return 0, fmt.Errorf("migration v%d: error beginning transaction: %w", schema.Version, err)
		}

		if _, err := tx.Exec(schema.SQL); err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("migration v%d (%s): error executing SQL: %w", schema.Version, schema.Description, err)
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
	}

	if _, err := db.Exec("PRAGMA optimize"); err != nil {
		return 0, fmt.Errorf("error optimizing database: %w", err)
	}

	return len(schemas) - int(current), nil
}
