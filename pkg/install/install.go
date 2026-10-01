// Package install versions, downloads, verifies and coordinates the two
// downloadable dependencies (exiftool, location database).
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
	"sync"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Phase names the dependency a Progress or DependencyError is about.
const (
	PhaseExiftool = "exiftool"
	PhaseLocation = "location"
)

// MaxTries is how many times Start tries to install the dependencies before
// giving up for this process.
const MaxTries = 3

// RetryDelay is how long the default BeforeRetry waits between tries.
const RetryDelay = 10 * time.Second

// Progress is one report about a dependency: bytes downloaded so far, or Ready
// once it is installed and verified.
type Progress struct {
	Phase       string
	Done, Total int64
	Ready       bool
}

// RetryFunc runs before try next (2..MaxTries) with the error that ended the
// previous one, and blocks until it is time to retry. A non-nil return stops
// trying.
type RetryFunc func(ctx context.Context, next int, err error) error

// Options configures a Coordinator. Log, OnProgress and BeforeRetry may be nil;
// a nil BeforeRetry logs the failure and waits RetryDelay.
type Options struct {
	ExecutablePath string // directory exiftool installs into
	LocationDBPath string // path to the location database file
	Log            logger.Logger

	OnProgress  func(Progress)
	BeforeRetry RetryFunc
}

// DependencyError is a failed install of one dependency.
type DependencyError struct {
	Phase string
	Err   error
}

func (e *DependencyError) Error() string { return e.Phase + ": " + e.Err.Error() }
func (e *DependencyError) Unwrap() error { return e.Err }

// Reason is the innermost cause, short enough for one line on screen.
func (e *DependencyError) Reason() string {
	err := e.Err
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			return err.Error()
		}
		err = inner
	}
}

// Coordinator installs exiftool and the location database under one install
// lock and hands out readiness through blocking getters. Construct with New.
type Coordinator struct {
	opts    Options
	started sync.Once   // Start closes the ready channels once
	running atomic.Bool // set once Start has run; Close waits only then

	exifPath  string
	exifErr   error
	exifReady chan struct{}

	resolver   *location.Resolver
	locationDB *db.ReadOnly
	locErr     error
	locReady   chan struct{}
}

// New returns a Coordinator ready for Start.
func New(opts Options) *Coordinator {
	if opts.Log == nil {
		opts.Log = logger.NewNoopLogger()
	}
	if opts.BeforeRetry == nil {
		opts.BeforeRetry = logAndWait(opts.Log)
	}
	return &Coordinator{
		opts:      opts,
		exifReady: make(chan struct{}),
		locReady:  make(chan struct{}),
	}
}

// Start installs exiftool then the location database in the background, up to
// MaxTries times. Only the first call does anything.
func (c *Coordinator) Start(ctx context.Context) {
	c.started.Do(func() { c.start(ctx) })
}

func (c *Coordinator) start(ctx context.Context) {
	c.running.Store(true)
	go func() {
		defer close(c.locReady)
		defer close(c.exifReady)

		l, err := c.acquireLock(ctx)
		if err != nil {
			c.exifErr, c.locErr = err, err
			return
		}
		defer l.Unlock()

		err = tryUpTo(ctx, MaxTries, c.opts.BeforeRetry, c.installMissing)
		if c.exifPath == "" {
			c.exifErr = err
		}
		if c.resolver == nil {
			c.locErr = err
		}
	}()
}

// installMissing is one try: each dependency not yet installed is installed,
// exiftool first. One failing doesn't stop the other; a retry skips what an
// earlier try finished.
func (c *Coordinator) installMissing(ctx context.Context) error {
	var errs []error
	if c.exifPath == "" {
		path, err := setupExiftool(ctx, c.opts.Log, c.opts.ExecutablePath, c.progressFor(PhaseExiftool))
		if err != nil {
			errs = append(errs, &DependencyError{Phase: PhaseExiftool, Err: err})
		} else {
			c.exifPath = path
			c.report(Progress{Phase: PhaseExiftool, Ready: true})
		}
	}
	if c.resolver == nil && ctx.Err() == nil {
		resolver, locationDB, err := OpenLocationResolver(ctx, c.opts.Log, c.opts.LocationDBPath, c.progressFor(PhaseLocation))
		if err != nil {
			errs = append(errs, &DependencyError{Phase: PhaseLocation, Err: err})
		} else {
			c.resolver, c.locationDB = resolver, locationDB
			c.report(Progress{Phase: PhaseLocation, Ready: true})
		}
	}
	return errors.Join(errs...)
}

// tryUpTo runs try until it succeeds, ctx ends, beforeRetry refuses, or tries
// runs out.
func tryUpTo(ctx context.Context, tries int, beforeRetry RetryFunc, try func(context.Context) error) error {
	var err error
	for n := 1; n <= tries; n++ {
		if n > 1 {
			if stop := beforeRetry(ctx, n, err); stop != nil {
				return stop
			}
		}
		if err = try(ctx); err == nil || ctx.Err() != nil {
			return err
		}
	}
	return fmt.Errorf("gave up after %d tries: %w", tries, err)
}

// logAndWait is the default RetryFunc: one warning, then RetryDelay.
func logAndWait(log logger.Logger) RetryFunc {
	return func(ctx context.Context, next int, err error) error {
		log.Warn(fmt.Sprintf("Download failed (try %d of %d). Switch to a better network if you can; retrying in %s",
			next-1, MaxTries, RetryDelay), logger.UserKey, true, "error", err)
		select {
		case <-time.After(RetryDelay):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Coordinator) acquireLock(ctx context.Context) (*lock.Lock, error) {
	installDir := filepath.Dir(c.opts.LocationDBPath)
	// try non-blocking first, so waiting can be announced rather than looking hung
	l, err := lock.AcquireInstall(ctx, installDir, false)
	if errors.Is(err, lock.ErrHeld) {
		c.opts.Log.Info("Waiting for another process to finish installing dependencies...", logger.UserKey, true)
		l, err = lock.AcquireInstall(ctx, installDir, true)
	}
	if err != nil {
		return nil, fmt.Errorf("wait for dependency install: %w", err)
	}
	return l, nil
}

// progressThrottle caps download progress reports to about ten a second.
const progressThrottle = 100 * time.Millisecond

func (c *Coordinator) report(p Progress) {
	if c.opts.OnProgress != nil {
		c.opts.OnProgress(p)
	}
}

// progressFor adapts a download's byte callback to a throttled OnProgress for
// one phase; nil when nobody listens.
func (c *Coordinator) progressFor(phase string) func(done, total int64) {
	if c.opts.OnProgress == nil {
		return nil
	}
	var last time.Time
	return func(done, total int64) {
		now := time.Now()
		if done < total && now.Sub(last) < progressThrottle {
			return
		}
		last = now
		c.report(Progress{Phase: phase, Done: done, Total: total})
	}
}

// ErrPending reports that a dependency is still installing. Only LocationNow
// returns it — the blocking getters wait instead.
var ErrPending = errors.New("dependency is still downloading")

// Exiftool blocks until the install is done (or ctx ends) and returns the
// binary's path.
func (c *Coordinator) Exiftool(ctx context.Context) (string, error) {
	if err := await(ctx, c.exifReady); err != nil {
		return "", err
	}
	return c.exifPath, c.exifErr
}

// Location blocks until the install is done (or ctx ends) and returns the
// location resolver.
func (c *Coordinator) Location(ctx context.Context) (*location.Resolver, error) {
	if err := await(ctx, c.locReady); err != nil {
		return nil, err
	}
	return c.resolver, c.locErr
}

// LocationNow returns the resolver without blocking. ErrPending means still
// installing (ask later); any other error means it never will.
func (c *Coordinator) LocationNow() (*location.Resolver, error) {
	select {
	case <-c.locReady:
		return c.resolver, c.locErr
	default:
		return nil, ErrPending
	}
}

// Close waits for a started install to finish (cancel its context first to
// stop it early) and closes the location database it opened.
func (c *Coordinator) Close() error {
	if !c.running.Load() {
		return nil
	}
	<-c.locReady
	if c.locationDB == nil {
		return nil
	}
	return c.locationDB.Close()
}

// await waits for ch to close, giving up when ctx is done.
func await(ctx context.Context, ch <-chan struct{}) error {
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// downloadStallTimeout aborts a download with no new bytes this long (a dead
// connection never errors itself). Armed before the request, so it also covers
// DNS/TCP/TLS/first byte.
const downloadStallTimeout = 3 * time.Second

// downloadFile fetches url to dest atomically, verifying wantSHA256 if set.
// One attempt: retrying is the Coordinator's job.
func downloadFile(ctx context.Context, dest, url, wantSHA256 string, onProgress func(done, total int64)) error {
	cleanStaleDownloads(filepath.Dir(dest))
	return terminalDownloadErr(ctx, downloadAttempt(ctx, dest, url, wantSHA256, onProgress))
}

// ErrDownloadStalled is a download that stopped making progress: the attempt's
// own timeout, not the caller's cancellation.
var ErrDownloadStalled = errors.New("download stalled")

// terminalDownloadErr is downloadFile's final error. A stall cancels the
// attempt's own context, so context.Canceled passes through only when the
// caller's ctx is actually done; otherwise a network failure would read as
// ctrl+c.
func terminalDownloadErr(ctx context.Context, err error) error {
	if err == nil || ctx.Err() != nil {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrDownloadStalled, err)
	}
	return err
}

// cleanStaleDownloads removes .dl-* temp files a killed process left. Best
// effort.
func cleanStaleDownloads(dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, ".dl-*"))
	if err != nil {
		return
	}
	for _, m := range matches {
		os.Remove(m)
	}
}

// downloadAttempt is downloadFile's one try, cancelled when no bytes arrive
// for downloadStallTimeout.
func downloadAttempt(ctx context.Context, dest, url, wantSHA256 string, onProgress func(done, total int64)) error {
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request %s: %w", url, err)
	}

	// stalled tells this attempt's stall guard apart from other cancellations,
	// which net/http reports identically
	var stalled atomic.Bool
	stall := time.AfterFunc(downloadStallTimeout, func() { stalled.Store(true); cancel() })
	defer stall.Stop()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if stalled.Load() {
			return fmt.Errorf("connection stalled: no data for %s", downloadStallTimeout)
		}
		return fmt.Errorf("connect: %w", err)
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

	src := &progressReader{r: resp.Body, total: resp.ContentLength, onProgress: func(done, total int64) {
		stall.Reset(downloadStallTimeout)
		if onProgress != nil {
			onProgress(done, total)
		}
	}}
	if _, err := io.Copy(tmp, src); err != nil {
		if stalled.Load() {
			return fmt.Errorf("connection stalled: no data for %s", downloadStallTimeout)
		}
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
			return fmt.Errorf("checksum mismatch for %s: got %s, want %s", filepath.Base(dest), sum, wantSHA256)
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

// openZstd opens path behind a zstd decoder (both dependencies ship zstd).
// Call the returned close func when done.
func openZstd(path string) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	zr, err := zstd.NewReader(f)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("zstd reader: %w", err)
	}
	return zr, func() { zr.Close(); f.Close() }, nil
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
