// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package exiftool

import (
	"context"
	"fmt"
	"sync"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
)

// Pool holds N long-lived -stay_open exiftool processes, checked out one
// at a time to match your goroutine concurrency.
type Pool struct {
	workers chan *Extractor
}

// NewPool starts size exiftool workers concurrently and returns a pool that
// hands them out one at a time. Starting them one at a time would pay every
// process's own startup cost (exiftool is a Perl script) back to back; doing
// it concurrently bounds the wait by the slowest one instead of the sum.
// Returns an error if any worker fails to start; any workers that did start
// are closed before returning.
func NewPool(exiftoolPath string, size int) (*Pool, error) {
	started := make([]*Extractor, size)
	errs := make([]error, size)
	var wg sync.WaitGroup
	for i := range size {
		wg.Go(func() {
			started[i], errs[i] = New(exiftoolPath)
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
	return &Pool{workers: workers}, nil
}

// Extract borrows an idle worker, runs the extraction, and returns the
// worker to the pool. Blocks if all workers are busy.
func (p *Pool) Extract(ctx context.Context, path string) (classifier.CommonMetadata, error) {
	select {
	case e := <-p.workers:
		defer func() { p.workers <- e }()
		return e.Extract(ctx, path)
	case <-ctx.Done():
		return classifier.CommonMetadata{}, ctx.Err()
	}
}

// Close shuts down every worker concurrently. Call once, after all in-flight
// Extract() calls have returned — the whole point of closing right when the
// metadata phase finishes is that it's prompt; waiting on each process's
// cmd.Wait() one at a time turned "done" into a slow tail.
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
