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

// All is the ordered list of migrations. Append new migrations at the end;
// never reorder or mutate existing entries.
var All = []Migration{
	{Version: 1, Description: "scanner schema", SQL: scannerSQL},
	{Version: 2, Description: "hasher schema", SQL: hasherSQL},
}

// Run applies any migrations whose version is greater than what the database
// has already recorded. It creates the schema_migrations tracking table on
// first run.
func Run(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			run_at  TEXT NOT NULL DEFAULT (datetime('now'))
		)
	`); err != nil {
		return fmt.Errorf("creating schema_migrations table: %w", err)
	}

	var current uint
	row := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`)
	if err := row.Scan(&current); err != nil {
		return fmt.Errorf("reading current migration version: %w", err)
	}

	for _, m := range All {
		if m.Version <= current {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("migration v%d: begin tx: %w", m.Version, err)
		}

		if _, err := tx.Exec(m.SQL); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration v%d (%s): %w", m.Version, m.Description, err)
		}

		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, run_at) VALUES (?, ?)`,
			m.Version, time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration v%d: recording version: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration v%d: commit: %w", m.Version, err)
		}
	}
	if _, err := db.Exec("PRAGMA optimize"); err != nil {
		return fmt.Errorf("optimizing database: %w", err)
	}

	return nil
}
