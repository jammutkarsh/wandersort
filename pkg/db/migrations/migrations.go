package migrations

import (
	"errors"
	"fmt"
	"slices"

	"github.com/jmoiron/sqlx"
)

// sqlNowDefault is the SQL DEFAULT for timestamps: local wall-clock time,
// millisecond precision padded to db.TimeLayout's 9-digit fraction.
const sqlNowDefault = `(strftime('%Y-%m-%dT%H:%M:%f000000','now','localtime'))`

// Migration describes a single schema migration step
type Migration struct {
	Version     uint
	Description string
	SQL         []string
}

// schemas is the ordered list of migrations. Append new migrations at the end;
// never reorder or mutate existing entries
var schemas = []Migration{schema001, schema002, schema003}

// ErrNewerSchema means a newer WanderSort wrote this database.
var ErrNewerSchema = errors.New("the library was written by a newer version of WanderSort; update WanderSort to open it")

// Pending reports how many migrations Run would apply, and how many are
// already applied (0 means a fresh database). It fails with ErrNewerSchema
// on a database carrying a version this build does not know.
func Pending(db *sqlx.DB) (pending, applied int, err error) {
	todo, done, err := plan(db)
	return len(todo), done, err
}

// plan reads the applied versions and returns the migrations still to run, in
// version order, and how many are already applied.
func plan(db *sqlx.DB) ([]Migration, int, error) {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			run_at  TEXT NOT NULL DEFAULT ` + sqlNowDefault + `
		) STRICT
	`); err != nil {
		return nil, 0, fmt.Errorf("error creating schema_migrations table: %w", err)
	}

	var versions []uint
	if err := db.Select(&versions, `SELECT version FROM schema_migrations`); err != nil {
		return nil, 0, fmt.Errorf("error reading applied migration versions: %w", err)
	}

	ordered := slices.Clone(schemas)
	slices.SortFunc(ordered, func(a, b Migration) int { return int(a.Version) - int(b.Version) })
	known := make(map[uint]bool, len(ordered))
	for i, m := range ordered {
		if i > 0 && m.Version == ordered[i-1].Version {
			return nil, 0, fmt.Errorf("duplicate migration version %d", m.Version)
		}
		known[m.Version] = true
	}
	applied := make(map[uint]bool, len(versions))
	for _, v := range versions {
		if !known[v] {
			return nil, 0, fmt.Errorf("%w (schema version %d)", ErrNewerSchema, v)
		}
		applied[v] = true
	}

	var todo []Migration
	for _, m := range ordered {
		if !applied[m.Version] {
			todo = append(todo, m)
		}
	}
	return todo, len(versions), nil
}

// Run applies any migrations not yet recorded in schema_migrations, in version
// order, each tracked individually. Refuses ErrNewerSchema first.
func Run(db *sqlx.DB) (int, error) {
	todo, _, err := plan(db)
	if err != nil {
		return 0, err
	}

	ran := 0
	for _, schema := range todo {
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

		// run_at takes the column default: the same wall-clock form as every
		// other stored timestamp
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version) VALUES (?)`, schema.Version,
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
