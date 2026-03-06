package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/exaring/otelpgx"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func InitDB(ctx context.Context, cfg config.Postgres, log logger.Logger) (*pgxpool.Pool, error) {
	dbName := cfg.DB
	if dbName == "" {
		dbName = "wandersort"
	}
	url := fmt.Sprintf("postgres://%s:%s@%s:%s/%s", cfg.User, cfg.Password, cfg.Host, cfg.Port, dbName)

	poolConfig, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("unable to parse database config: %w", err)
	}
	poolConfig.ConnConfig.Tracer = otelpgx.NewTracer(otelpgx.WithTrimSQLInSpanName())

	dbpool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to database: %w", err)
	}
	if err := dbpool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("unable to ping database: %w", err)
	}

	log.Info("Database connection established", "host", cfg.Host, "port", cfg.Port, "user", cfg.User)

	// Run migrations using sql.DB
	if err := runMigrations(cfg, log); err != nil {
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	log.Info("Successfully connected to postgres database")
	return dbpool, nil
}

// runMigrations applies database migrations using embedded SQL files.
func runMigrations(cfg config.Postgres, log logger.Logger) error {
	dbName := cfg.DB
	if dbName == "" {
		dbName = "wandersort"
	}

	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, dbName)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("opening database for migrations: %w", err)
	}
	defer db.Close()

	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("creating migrate driver: %w", err)
	}

	// Use the embedded filesystem so migrations work identically in Docker
	// and local builds — no filesystem path dependency.
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("creating iofs source: %w", err)
	}

	log.Info("Applying embedded migrations")
	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return fmt.Errorf("creating migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("applying migrations: %w", err)
	}

	log.Info("All database migrations applied successfully")
	return nil
}
