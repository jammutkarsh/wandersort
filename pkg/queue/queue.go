// Package queue owns all River setup so main.go only needs to call New and Stop.
package queue

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// Queue name constants. Use these everywhere instead of raw string literals
// so a rename stays a single-line change.
const (
	ScanQueue = "file_scanning"
	HashQueue = "file_hashing"
)

// Config holds queue-level configuration.
type Config struct {
	MaxConcurrentScans   int // Concurrent scan workers (default: 5)
	MaxConcurrentHashers int // Concurrent hash workers (default: 4)
}

// Enqueuer is the minimal job-dispatch capability.
// Pass it to any service that needs to enqueue jobs (e.g. Scanner).
type Enqueuer interface {
	Enqueue(ctx context.Context, args river.JobArgs) error
}

// New wires up River:
//   - runs any pending schema migrations
//   - creates and starts the River client with the provided workers
//   - returns the client (for shutdown) and an Enqueuer (for dispatching jobs)
//
// The caller registers workers via river.AddWorker before calling New:
//
//	workers := river.NewWorkers()
//	river.AddWorker(workers, &scanner.ScanTaskWorker{...})
//	river.AddWorker(workers, &hasher.HashTaskWorker{...})
//	client, enqueuer, err := queue.New(ctx, pool, cfg, workers)
func New(
	ctx context.Context,
	pool *pgxpool.Pool,
	cfg Config,
	workers *river.Workers,
) (*river.Client[pgx.Tx], Enqueuer, error) {
	if err := migrate(ctx, pool); err != nil {
		return nil, nil, fmt.Errorf("river migration: %w", err)
	}

	// Defaults
	if cfg.MaxConcurrentScans == 0 {
		cfg.MaxConcurrentScans = 5
	}
	if cfg.MaxConcurrentHashers == 0 {
		cfg.MaxConcurrentHashers = 4
	}

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			ScanQueue: {MaxWorkers: cfg.MaxConcurrentScans},
			HashQueue: {MaxWorkers: cfg.MaxConcurrentHashers},
		},
		Workers: workers,
	})
	if err != nil {
		return nil, nil, err
	}

	if err := client.Start(ctx); err != nil {
		return nil, nil, err
	}

	return client, &enqueuer{client: client}, nil
}

// migrate runs any pending River schema migrations.
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return err
	}
	res, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return err
	}
	for _, v := range res.Versions {
		log.Printf("river migration applied: version %d", v.Version)
	}
	return nil
}

// enqueuer wraps *river.Client[pgx.Tx] behind the Enqueuer interface.
type enqueuer struct {
	client *river.Client[pgx.Tx]
}

func (e *enqueuer) Enqueue(ctx context.Context, args river.JobArgs) error {
	_, err := e.client.Insert(ctx, args, nil)
	return err
}
