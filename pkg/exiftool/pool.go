package exiftool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
)

// Pool holds N long-lived -stay_open exiftool processes, checked out one
// at a time to match your goroutine concurrency.
type Pool struct {
	path    string
	timeout time.Duration
	workers chan *Extractor
}

// NewPool starts size exiftool workers and returns a pool handing them out
// one at a time. On any start failure, workers that did start are closed.
func NewPool(exiftoolPath string, size int) (*Pool, error) {
	return newPool(exiftoolPath, size, extractTimeout)
}

func newPool(exiftoolPath string, size int, timeout time.Duration) (*Pool, error) {
	started := make([]*Extractor, size)
	errs := make([]error, size)
	var wg sync.WaitGroup
	for i := range size {
		// concurrent start bounds the wait by the slowest process (exiftool
		// is a Perl script with real startup cost), not the sum of all N
		wg.Go(func() {
			started[i], errs[i] = newExtractor(exiftoolPath, timeout)
		})
	}
	wg.Wait()

	workers := make(chan *Extractor, size)
	var firstErr error
	for i, err := range errs {
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("starting exiftool worker %d: %w", i, err)
			continue
		}
		if started[i] != nil {
			workers <- started[i]
		}
	}
	if firstErr != nil {
		close(workers)
		for w := range workers {
			w.Close()
		}
		return nil, firstErr
	}
	return &Pool{path: exiftoolPath, timeout: timeout, workers: workers}, nil
}

// Extract borrows an idle worker (replacing a dead one first), extracts, and
// returns it. Blocks while all are busy.
func (p *Pool) Extract(ctx context.Context, path string) (classifier.CommonMetadata, error) {
	select {
	case e := <-p.workers:
		if e.Dead() {
			fresh, err := newExtractor(p.path, p.timeout)
			if err != nil {
				p.workers <- e // keep the pool its size; the next caller retries
				return classifier.CommonMetadata{}, fmt.Errorf("%w: restarting worker: %w", ErrProcess, err)
			}
			e.Close() // already killed, so this only reaps it
			e = fresh
		}
		defer func() { p.workers <- e }()
		return e.Extract(ctx, path)
	case <-ctx.Done():
		return classifier.CommonMetadata{}, ctx.Err()
	}
}

// Close shuts down every worker concurrently. Call once, after all Extract
// calls have returned.
func (p *Pool) Close() error {
	close(p.workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for e := range p.workers {
		wg.Go(func() {
			if err := e.Close(); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return firstErr
}
