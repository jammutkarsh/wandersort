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
	// OnProgress receives download byte progress, phase "exiftool" or
	// "location". Not called for a dependency already on disk.
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

		c.exifPath, c.exifErr = c.installExiftool(ctx)
		close(c.exifReady)
		if c.exifErr != nil {
			close(c.locReady)
			return
		}

		c.resolver, c.locationDB, c.locErr = c.installLocation(ctx)
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

		c.resolver, c.locationDB, c.locErr = c.installLocation(ctx)
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

func (c *Coordinator) installExiftool(ctx context.Context) (string, error) {
	path, err := setupExiftool(ctx, c.opts.Log, c.opts.ExecutablePath, c.progressFor("exiftool"))
	if err != nil {
		return "", fmt.Errorf("exiftool: %w", err)
	}
	return path, nil
}

func (c *Coordinator) installLocation(ctx context.Context) (*location.Resolver, *db.DB, error) {
	return OpenLocationResolver(ctx, c.opts.Log, c.opts.LocationDBPath, c.progressFor("location"))
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

// Download fetches url and writes the body to dest atomically (via a temp file
// in the same directory) so a partial or tampered download never leaves a bad
// file at dest.
//
// onProgress (may be nil) is invoked as bytes arrive with (bytesSoFar,
// totalBytes); total is -1 when the server sends no Content-Length. It runs on
// the download goroutine and must not block.
//
// wantSHA256 (may be empty) is the expected hex digest. A mismatch removes dest
// and returns an error.
func Download(ctx context.Context, dest, url, wantSHA256 string, onProgress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
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

	var src io.Reader = resp.Body
	if onProgress != nil {
		src = &progressReader{r: resp.Body, total: resp.ContentLength, onProgress: onProgress}
	}
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
		sum, err := SHA256File(dest)
		if err != nil {
			os.Remove(dest)
			return fmt.Errorf("checksum %s: %w", dest, err)
		}
		if sum != wantSHA256 {
			os.Remove(dest)
			return fmt.Errorf("checksum mismatch for %s: got %s, want %s", filepath.Base(dest), sum, wantSHA256)
		}
	}

	return nil
}

// SHA256File returns the hex-encoded SHA-256 hash of the file at path.
func SHA256File(path string) (string, error) {
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
