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

// Phase names Options.OnProgress reports under.
const (
	PhaseExiftool = "exiftool"
	PhaseLocation = "location"
)

// Options configures a Coordinator. Log and OnProgress may be nil.
type Options struct {
	ExecutablePath string // directory exiftool installs into
	LocationDBPath string // path to the location database file
	Log            logger.Logger

	OnProgress func(phase string, done, total int64)
}

// Coordinator installs exiftool and the location database under one install
// lock and hands out readiness through blocking getters. Construct with New.
type Coordinator struct {
	opts    Options
	started sync.Once   // Start/StartLocationOnly close the ready channels once
	running atomic.Bool // set once a Start has run; Close waits only then

	exifPath  string
	exifErr   error
	exifReady chan struct{}

	resolver   *location.Resolver
	locationDB *db.ReadOnly
	locErr     error
	locReady   chan struct{}
}

// New returns a Coordinator ready for Start or StartLocationOnly.
func New(opts Options) *Coordinator {
	if opts.Log == nil {
		opts.Log = logger.NewNoopLogger()
	}
	return &Coordinator{
		opts:      opts,
		exifReady: make(chan struct{}),
		locReady:  make(chan struct{}),
	}
}

// Start installs exiftool then the location database in the background.
// Only the first Start or StartLocationOnly call does anything.
func (c *Coordinator) Start(ctx context.Context) {
	c.started.Do(func() { c.start(ctx) })
}

func (c *Coordinator) start(ctx context.Context) {
	c.running.Store(true)
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
		c.exifPath, c.exifErr = setupExiftool(ctx, c.opts.Log, c.opts.ExecutablePath, c.progressFor(PhaseExiftool))
		if c.exifErr != nil {
			c.exifErr = fmt.Errorf("exiftool: %w", c.exifErr)
		}
		close(c.exifReady)
		if c.exifErr != nil {
			c.locErr = fmt.Errorf("location database not installed: %w", c.exifErr)
			close(c.locReady)
			return
		}

		c.resolver, c.locationDB, c.locErr = OpenLocationResolver(ctx, c.opts.Log, c.opts.LocationDBPath, c.progressFor(PhaseLocation))
		close(c.locReady)
	}()
}

// StartLocationOnly installs just the location database. onReady, if not nil,
// runs once it resolves.
func (c *Coordinator) StartLocationOnly(ctx context.Context, onReady func(error)) {
	c.started.Do(func() { c.startLocationOnly(ctx, onReady) })
}

func (c *Coordinator) startLocationOnly(ctx context.Context, onReady func(error)) {
	c.running.Store(true)
	c.exifErr = errExiftoolNotInstalled
	close(c.exifReady)
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

		c.resolver, c.locationDB, c.locErr = OpenLocationResolver(ctx, c.opts.Log, c.opts.LocationDBPath, c.progressFor(PhaseLocation))
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
		c.opts.Log.Info("Waiting for another process to finish installing dependencies...", logger.UserKey, true)
		l, err = lock.AcquireInstall(ctx, installDir, true)
	}
	if err != nil {
		return nil, fmt.Errorf("wait for dependency install: %w", err)
	}
	return l, nil
}

// progressThrottle caps how often byte progress reaches the UI: an unthrottled
// burst of chunks blocks bubbletea's message loop and starves other phases.
const progressThrottle = 100 * time.Millisecond

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
		c.opts.OnProgress(phase, done, total)
	}
}

// errExiftoolNotInstalled is what Exiftool reports on a Coordinator started
// with StartLocationOnly.
var errExiftoolNotInstalled = errors.New("exiftool is not installed by this coordinator")

// ErrPending reports that a dependency is still installing. Only LocationNow
// returns it — the blocking getters wait instead.
var ErrPending = errors.New("dependency is still downloading")

// Exiftool blocks until the binary is ready, logging "Waiting for …" only if
// it actually has to wait.
func (c *Coordinator) Exiftool(ctx context.Context) (string, error) {
	if err := c.awaitLog(ctx, c.exifReady, "Waiting for the exiftool download to finish…"); err != nil {
		return "", err
	}
	return c.exifPath, c.exifErr
}

// Location blocks until the location resolver is ready, with the same
// "say so only if it actually blocks" behaviour as Exiftool.
func (c *Coordinator) Location(ctx context.Context) (*location.Resolver, error) {
	if err := c.awaitLog(ctx, c.locReady, "Waiting for the location database download to finish…"); err != nil {
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

// awaitLog waits on ch, logging why only if it isn't already closed, and gives
// up when ctx is done.
func (c *Coordinator) awaitLog(ctx context.Context, ch <-chan struct{}, why string) error {
	select {
	case <-ch:
		return nil
	default:
	}
	c.opts.Log.Info(why, logger.UserKey, true)
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

const (
	// downloadStallTimeout aborts an attempt with no new bytes this long (a
	// dead connection never errors itself). Armed before the request, so it
	// also covers DNS/TCP/TLS/first byte.
	downloadStallTimeout = 3 * time.Second

	// downloadBackoffBase/Max bound the exponential retry delay
	downloadBackoffBase = 1 * time.Second
	downloadBackoffMax  = 8 * time.Second
)

// nonRetryable marks a download failure retrying can't fix (bad status code,
// checksum mismatch).
type nonRetryable struct{ err error }

func (n *nonRetryable) Error() string { return n.err.Error() }
func (n *nonRetryable) Unwrap() error { return n.err }

// downloadFile fetches url to dest atomically, verifying wantSHA256 if set.
// Transport failures retry forever with backoff until success or ctx is
// cancelled.
func downloadFile(ctx context.Context, log logger.Logger, dest, url, wantSHA256 string, onProgress func(done, total int64)) error {
	cleanStaleDownloads(filepath.Dir(dest))

	for attempt := 1; ; attempt++ {
		err := downloadAttempt(ctx, dest, url, wantSHA256, onProgress)
		if err == nil {
			return nil
		}
		// A bad status/checksum fails identically every time; only a
		// transport failure is worth retrying.
		var nr *nonRetryable
		if errors.As(err, &nr) || ctx.Err() != nil {
			return terminalDownloadErr(ctx, err)
		}
		if log != nil {
			log.Warn("Download failed, retrying", logger.UserKey, true,
				"url", url, "attempt", attempt, "error", err)
		}
		// exponent capped at 3 (1<<3 * base == downloadBackoffMax already) so
		// an attempt count that climbs for hours never overflows the shift.
		delay := min(downloadBackoffBase*time.Duration(1<<min(attempt-1, 3)), downloadBackoffMax)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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

// downloadAttempt is one try at downloadFile, cancelled when no bytes arrive
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
