// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package exiftool

import (
	"context"
	"fmt"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
)

// Pool holds N long-lived -stay_open exiftool processes, checked out one
// at a time to match your goroutine concurrency.
type Pool struct {
	workers chan *Extractor
}

// NewPool starts size exiftool workers and returns a pool that hands them
// out one at a time. Returns an error if any worker fails to start; any
// workers already started are closed before returning.
func NewPool(exiftoolPath string, size int) (*Pool, error) {
	workers := make(chan *Extractor, size)
	for i := range size {
		e, err := New(exiftoolPath)
		if err != nil {
			close(workers)
			for w := range workers {
				w.Close()
			}
			return nil, fmt.Errorf("starting exiftool worker %d: %w", i, err)
		}
		workers <- e
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

// Close shuts down every worker. Call once, after all in-flight
// Extract() calls have returned.
func (p *Pool) Close() error {
	close(p.workers)
	var firstErr error
	for e := range p.workers {
		if err := e.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
