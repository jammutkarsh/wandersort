// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package install is the one place wandersort's two downloadable
// dependencies — the exiftool binary and the location gazetteer — are
// versioned, located, downloaded, verified, and coordinated for every
// command that needs them (scan, review, the config wizard). pkg/exiftool
// and pkg/location only ever run the already-installed binary or query an
// already-open, already-verified database; neither knows what version it
// needs, where it downloads from, or what its on-disk layout looks like —
// that's entirely this package's job (exiftool_setup.go, location_setup.go).
//
// Before this package existed, each command also hand-rolled its own
// goroutine, channel pair, and progress callback, mutating a handful of
// *app fields a background goroutine wrote and a pipeline goroutine read,
// with the happens-before edge documented in a comment rather than enforced
// by a type. A Coordinator owns the install order (exiftool first, the small
// download; the gazetteer behind it, since only the last pipeline phase
// needs it), the shared install lock, the download progress fan-out, and
// readiness — callers only ever see blocking or non-blocking getters, never
// a raw channel.
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

// Start installs exiftool then the location database, in that order, under
// the install lock, and returns immediately — the goroutine runs in the
// background so a caller (a scan's own file walk and hashing) can proceed
// concurrently. Each dependency's readiness is exposed independently
// (Exiftool/AwaitExiftool vs Location/AwaitLocation), so a pipeline phase
// gating only on exiftool never waits on the location database too.
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

// StartLocationOnly installs just the location database, under the same
// install lock — for a caller (the config wizard) with no use for exiftool.
// onReady, if not nil, is called once the location database has resolved
// (success or failure).
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

// Exiftool blocks until the exiftool binary is ready, silently.
func (c *Coordinator) Exiftool() (string, error) {
	<-c.exifReady
	return c.exifPath, c.exifErr
}

// AwaitExiftool blocks until the exiftool binary is ready, logging why only
// if the call actually has to wait — installed dependencies leave no trace.
// For a caller (a pipeline phase) that may be stalled behind its own
// process's still-running download, not a competing process.
func (c *Coordinator) AwaitExiftool() (string, error) {
	c.awaitLog(c.exifReady, "Waiting for the exiftool download to finish…")
	return c.exifPath, c.exifErr
}

// Location blocks until the location resolver is ready, silently.
func (c *Coordinator) Location() (*location.Resolver, error) {
	<-c.locReady
	return c.resolver, c.locErr
}

// AwaitLocation is Location with the same "log only if it actually blocks"
// behavior as AwaitExiftool.
func (c *Coordinator) AwaitLocation() (*location.Resolver, error) {
	c.awaitLog(c.locReady, "Waiting for the location database download to finish…")
	return c.resolver, c.locErr
}

// LocationReady reports whether Location/AwaitLocation would return
// immediately, without blocking — for a caller (a form validator running on
// every keystroke) that must never wait on a download.
func (c *Coordinator) LocationReady() bool {
	select {
	case <-c.locReady:
		return true
	default:
		return false
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
	// downloadStallTimeout aborts an attempt that has gone this long without
	// a single new byte arriving. A network switch (e.g. wifi to a different
	// AP, or wifi to ethernet) can leave the underlying TCP connection dead
	// without the OS or the server ever telling us, so io.Copy would
	// otherwise block forever instead of erroring out to a retry. The whole
	// pipeline finishes in about a minute end to end, so a stalled download
	// eating even a few seconds of that is worth noticing fast.
	downloadStallTimeout = 1 * time.Second
)

// nonRetryable marks a download failure retrying can't fix — a bad URL
// (status code) or a checksum mismatch will fail the exact same way every
// time, so downloadFile gives up after the first attempt instead of wasting
// downloadMaxAttempts-1 retries and their backoff delays on it.
type nonRetryable struct{ err error }

func (n *nonRetryable) Error() string { return n.err.Error() }
func (n *nonRetryable) Unwrap() error { return n.err }

// downloadFile fetches url and writes the body to dest atomically (via a temp
// file in the same directory) so a partial or tampered download never leaves
// a bad file at dest. Retries on a transport failure (see downloadMaxAttempts),
// starting the download over from byte zero each time; log (may be nil) gets
// a UserKey line on each retry so a stalled download reads as "retrying", not
// as a frozen progress bar.
//
// onProgress (may be nil) is invoked as bytes arrive with (bytesSoFar,
// totalBytes); total is -1 when the server sends no Content-Length. It runs on
// the download goroutine and must not block.
//
// wantSHA256 (may be empty) is the expected hex digest. A mismatch removes dest
// and returns an error.
func downloadFile(ctx context.Context, log logger.Logger, dest, url, wantSHA256 string, onProgress func(done, total int64)) error {
	cleanStaleDownloads(filepath.Dir(dest))

	var err error
	for attempt := 1; attempt <= downloadMaxAttempts; attempt++ {
		err = downloadAttempt(ctx, dest, url, wantSHA256, onProgress)
		if err == nil {
			return nil
		}
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

// cleanStaleDownloads removes any .dl-* temp file left behind in dir by a
// process that was killed (not exited normally) mid-download — a graceful
// exit already cleans its own up via defer, but SIGKILL/panic doesn't run
// that. The random suffix os.CreateTemp picks means the exact name varies
// per run, so this globs the fixed prefix rather than tracking one name.
// Best effort: a leftover here is disk clutter, not a correctness problem
// the next download depends on.
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
