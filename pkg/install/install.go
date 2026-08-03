// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package install versions, downloads, verifies, and coordinates
// wandersort's two downloadable dependencies (exiftool, location database)
// for every command that needs them. pkg/exiftool and pkg/location only run
// the already-installed binary / query the already-open DB — this package
// owns all version/URL/layout knowledge instead.
package install

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Options configures a Coordinator. Log and OnProgress may be nil.
type Options struct {
	ExecutablePath string // directory exiftool installs into
	LocationDBPath string // path to the location database file
	Log            logger.Logger

	OnProgress func(phase string, done, total int64)
}

// Coordinator installs exiftool and the location database under one install
// lock and hands out their readiness through typed getters. Zero value is
// not usable; construct with New.
type Coordinator struct {
	opts Options

	exifPath  string
	exifErr   error
	exifReady chan struct{}

	resolver   *location.Resolver
	locationDB *db.DB
	locErr     error
	locReady   chan struct{}
}

// New returns a Coordinator ready for Start or StartLocationOnly.
func New(opts Options) *Coordinator {
	return &Coordinator{
		opts:      opts,
		exifReady: make(chan struct{}),
		locReady:  make(chan struct{}),
	}
}

// Start installs exiftool then the location database and returns
// immediately; the goroutine runs so scan/hash can proceed concurrently.
func (c *Coordinator) Start(ctx context.Context) {
	go func() {
		l, err := c.acquireLock(ctx)
		if err != nil {
			c.exifErr, c.locErr = err, err
			close(c.exifReady)
			close(c.locReady)
			return
		}
		defer l.Unlock()

		// exiftool first: it's the small download the earlier exif phase
		// waits on; the location DB has the whole pipeline to hide behind.
		c.exifPath, c.exifErr = setupExiftool(ctx, c.opts.Log, c.opts.ExecutablePath, c.progressFor("exiftool"))
		if c.exifErr != nil {
			c.exifErr = fmt.Errorf("exiftool: %w", c.exifErr)
		}
		close(c.exifReady)
		if c.exifErr != nil {
			close(c.locReady)
			return
		}

		c.resolver, c.locationDB, c.locErr = OpenLocationResolver(ctx, c.opts.Log, c.opts.LocationDBPath, c.progressFor("location"))
		close(c.locReady)
	}()
}

// StartLocationOnly installs just the location database — for a caller (the
// config wizard) with no use for exiftool. onReady, if not nil, runs once
// the database has resolved, success or failure.
func (c *Coordinator) StartLocationOnly(ctx context.Context, onReady func(error)) {
	close(c.exifReady) // nothing waits on exiftool through this Coordinator
	go func() {
		l, err := c.acquireLock(ctx)
		if err != nil {
			c.locErr = err
			close(c.locReady)
			if onReady != nil {
				onReady(err)
			}
			return
		}
		defer l.Unlock()

		c.resolver, c.locationDB, c.locErr = OpenLocationResolver(ctx, c.opts.Log, c.opts.LocationDBPath, c.progressFor("location"))
		close(c.locReady)
		if onReady != nil {
			onReady(c.locErr)
		}
	}()
}

func (c *Coordinator) acquireLock(ctx context.Context) (*lock.Lock, error) {
	installDir := filepath.Dir(c.opts.LocationDBPath)
	// try non-blocking first, so waiting can be announced rather than looking hung
	l, err := lock.AcquireInstall(ctx, installDir, false)
	if errors.Is(err, lock.ErrHeld) {
		if c.opts.Log != nil {
			c.opts.Log.Info("Waiting for another process to finish installing dependencies...", logger.UserKey, true)
		}
		l, err = lock.AcquireInstall(ctx, installDir, true)
	}
	if err != nil {
		return nil, fmt.Errorf("wait for dependency install: %w", err)
	}
	return l, nil
}

func (c *Coordinator) progressFor(phase string) func(done, total int64) {
	if c.opts.OnProgress == nil {
		return nil
	}
	return func(done, total int64) { c.opts.OnProgress(phase, done, total) }
}

// ErrPending reports that a dependency is still installing. Only LocationNow
// returns it — the blocking getters wait instead.
var ErrPending = errors.New("dependency is still downloading")

// Exiftool blocks until the exiftool binary is ready, saying so only if the
// call actually has to wait. Narration is a property of waiting, not of who
// asked: a caller that finds the binary already installed sees nothing, and
// one stalled behind its own process's still-running download is told why
// rather than looking hung.
func (c *Coordinator) Exiftool() (string, error) {
	c.awaitLog(c.exifReady, "Waiting for the exiftool download to finish…")
	return c.exifPath, c.exifErr
}

// Location blocks until the location resolver is ready, with the same
// "say so only if it actually blocks" behaviour as Exiftool.
func (c *Coordinator) Location() (*location.Resolver, error) {
	c.awaitLog(c.locReady, "Waiting for the location database download to finish…")
	return c.resolver, c.locErr
}

// LocationNow returns the resolver without ever blocking — for a caller (a
// form validator running on every keystroke) that cannot wait on a download.
// ErrPending means the install is still running and asking again later may
// work; any other error means it never will, which is a different answer: a
// wizard holds a field on the first and waves it through on the second.
func (c *Coordinator) LocationNow() (*location.Resolver, error) {
	select {
	case <-c.locReady:
		return c.resolver, c.locErr
	default:
		return nil, ErrPending
	}
}

// LocationDBIfReady returns the opened location database handle without
// blocking, or nil if Location hasn't resolved yet (or was never started) —
// for a caller (closeDBs) that must never wait on a download just to shut
// down cleanly.
func (c *Coordinator) LocationDBIfReady() *db.DB {
	select {
	case <-c.locReady:
		return c.locationDB
	default:
		return nil
	}
}

// awaitLog logs why only if ch isn't already closed — a caller stalled
// behind its own process's still-running download, not a competing one.
func (c *Coordinator) awaitLog(ch <-chan struct{}, why string) {
	select {
	case <-ch:
		return
	default:
	}
	if c.opts.Log != nil {
		c.opts.Log.Info(why, logger.UserKey, true)
	}
	<-ch
}

const (
	// downloadMaxAttempts bounds retries on a transport failure (dropped or
	// stalled connection, DNS hiccup) — not on a bad response, see
	// nonRetryable below.
	downloadMaxAttempts = 4
	// downloadStallTimeout aborts an attempt with no new bytes in this long —
	// a dead TCP connection (e.g. wifi→ethernet switch) never tells us
	// itself, so io.Copy would otherwise hang forever instead of retrying.
	// It's armed before the request is even sent, so it also has to cover
	// DNS+TCP+TLS+first-byte — 1s was too tight for that on a real (non-loopback)
	// network and turned ordinary latency into spurious retry storms.
	downloadStallTimeout = 3 * time.Second
)

// nonRetryable marks a download failure retrying can't fix — a bad URL
// (status code) or a checksum mismatch will fail the exact same way every
// time, so downloadFile gives up after the first attempt instead of wasting
// downloadMaxAttempts-1 retries and their backoff delays on it.
type nonRetryable struct{ err error }

func (n *nonRetryable) Error() string { return n.err.Error() }
func (n *nonRetryable) Unwrap() error { return n.err }

// downloadFile fetches url to dest atomically, verifying wantSHA256 if set,
// and retries on transport failure (downloadMaxAttempts). onProgress and
// wantSHA256 may be nil/empty.
func downloadFile(ctx context.Context, log logger.Logger, dest, url, wantSHA256 string, onProgress func(done, total int64)) error {
	cleanStaleDownloads(filepath.Dir(dest))

	var err error
	for attempt := 1; attempt <= downloadMaxAttempts; attempt++ {
		err = downloadAttempt(ctx, dest, url, wantSHA256, onProgress)
		if err == nil {
			return nil
		}
		// A bad status/checksum fails identically every time; only a
		// transport failure is worth spending a retry on.
		var nr *nonRetryable
		if errors.As(err, &nr) || ctx.Err() != nil || attempt == downloadMaxAttempts {
			return err
		}
		if log != nil {
			log.Warn("Download failed, retrying", logger.UserKey, true,
				"url", url, "attempt", attempt, "of", downloadMaxAttempts, "error", err)
		}
		select {
		case <-time.After(time.Duration(attempt) * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

// cleanStaleDownloads removes .dl-* temp files a killed process (SIGKILL,
// panic) left behind — a graceful exit already cleans its own up via defer.
// Best effort: a leftover is disk clutter, not a correctness problem.
func cleanStaleDownloads(dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, ".dl-*"))
	if err != nil {
		return
	}
	for _, m := range matches {
		os.Remove(m)
	}
}

// downloadAttempt is one try at downloadFile's job. It cancels its own
// request if downloadStallTimeout passes with no progress, turning a dead
// connection into a prompt, retryable error instead of an indefinite hang.
func downloadAttempt(ctx context.Context, dest, url, wantSHA256 string, onProgress func(done, total int64)) error {
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request %s: %w", url, err)
	}

	stall := time.AfterFunc(downloadStallTimeout, cancel)
	defer stall.Stop()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &nonRetryable{fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)}
	}

	// Write to a temp file in the same directory so os.Rename is atomic
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".dl-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op if Rename succeeded
	}()

	src := &progressReader{r: resp.Body, total: resp.ContentLength, onProgress: func(done, total int64) {
		stall.Reset(downloadStallTimeout)
		if onProgress != nil {
			onProgress(done, total)
		}
	}}
	if _, err := io.Copy(tmp, src); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("rename to %s: %w", dest, err)
	}

	if wantSHA256 != "" {
		sum, err := fileSHA256(dest)
		if err != nil {
			os.Remove(dest)
			return fmt.Errorf("checksum %s: %w", dest, err)
		}
		if sum != wantSHA256 {
			os.Remove(dest)
			return &nonRetryable{fmt.Errorf("checksum mismatch for %s: got %s, want %s", filepath.Base(dest), sum, wantSHA256)}
		}
	}

	return nil
}

// fileSHA256 returns the hex-encoded SHA-256 hash of the file at path.
func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// progressReader reports cumulative bytes read to onProgress as they flow.
type progressReader struct {
	r          io.Reader
	total      int64
	done       int64
	onProgress func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.done += int64(n)
		p.onProgress(p.done, p.total)
	}
	return n, err
}
