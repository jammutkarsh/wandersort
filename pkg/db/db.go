package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/exaring/otelpgx"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	_ "github.com/lib/pq"
)

func InitDB(ctx context.Context, cfg config.Postgres, log logger.Logger) *pgxpool.Pool {
	dbName := cfg.DB
	if dbName == "" {
		dbName = "wandersort"
	}
	url := fmt.Sprintf("postgres://%s:%s@%s:%s/%s", cfg.User, cfg.Password, cfg.Host, cfg.Port, dbName)

	poolConfig, err := pgxpool.ParseConfig(url)
	if err != nil {
		log.Error("unable to parse database config", "error", err)
		return nil
	}
	poolConfig.ConnConfig.Tracer = otelpgx.NewTracer(otelpgx.WithTrimSQLInSpanName())

	dbpool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		log.Error("unable to connect to database", "error", err)
		return nil
	}
	if err := dbpool.Ping(ctx); err != nil {
		log.Error("unable to ping database", "error", err)
		return nil
	}

	log.Info("Database connection established", "host", cfg.Host, "port", cfg.Port, "user", cfg.User)

	// Run migrations using sql.DB
	if err := runMigrations(cfg, log); err != nil {
		log.Error("running migrations", "error", err)
		return nil
	}

	log.Info("Successfully connected to postgres database")
	return dbpool
}

// runMigrations applies database migrations
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

	// migrations are stored in pkg/db/migrations
	migrationPath := "file://pkg/db/migrations"

	log.Info("Applying migrations", "path", migrationPath)
	m, err := migrate.NewWithDatabaseInstance(
		migrationPath,
		"postgres",
		driver,
	)
	if err != nil {
		return fmt.Errorf("creating migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("applying migrations: %w", err)
	}

	log.Info("All database migrations applied successfully")
	return nil
}
