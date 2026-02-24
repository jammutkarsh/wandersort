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

// Config holds queue-level configuration.
type Config struct {
	MaxConcurrentScans int
}

// Enqueuer is the job-dispatch capability injected into workers after the client starts.
type Enqueuer interface {
	Enqueue(ctx context.Context, args interface{ Kind() string }) error
}

// Worker is implemented by every job worker that wants to participate in the queue.
// Register adds the worker to River's worker registry.
// SetEnqueuer receives the live client so the worker's domain object can dispatch jobs.
type Worker interface {
	Register(workers *river.Workers)
	SetEnqueuer(e Enqueuer)
}

// New wires up River completely:
//   - runs any pending schema migrations
//   - registers all provided workers
//   - creates and starts the River client
//   - injects the enqueuer into each worker
//
// The caller is only responsible for calling client.Stop(ctx) on shutdown.
func New(ctx context.Context, pool *pgxpool.Pool, cfg Config, ww ...Worker) (*river.Client[pgx.Tx], error) {
	if err := migrate(ctx, pool); err != nil {
		return nil, fmt.Errorf("river migration: %w", err)
	}

	riverWorkers := river.NewWorkers()
	for _, w := range ww {
		w.Register(riverWorkers)
	}

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: cfg.MaxConcurrentScans},
		},
		Workers: riverWorkers,
	})
	if err != nil {
		return nil, err
	}

	enq := riverEnqueuer{client: client}
	for _, w := range ww {
		w.SetEnqueuer(enq)
	}

	if err := client.Start(ctx); err != nil {
		return nil, err
	}

	return client, nil
}

// migrate runs any pending River schema migrations (internal helper).
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

// riverEnqueuer wraps *river.Client. Exported as a value type so worker packages
// can embed or store it without importing the full River client.
type riverEnqueuer struct {
	client *river.Client[pgx.Tx]
}

func (r riverEnqueuer) Enqueue(ctx context.Context, args interface{ Kind() string }) error {
	_, err := r.client.Insert(ctx, args, nil)
	return err
}
