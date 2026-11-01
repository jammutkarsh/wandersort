package exiftool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
)

// TestPoolStartsAndClosesConcurrently covers the concurrent NewPool/Close
// path: every worker must come up and every worker must shut down, even
// though both now happen in parallel goroutines rather than one at a time.
func TestPoolStartsAndClosesConcurrently(t *testing.T) {
	const size = 4
	p, err := NewPool(installtest.Exiftool(t), size)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	if got := len(p.workers); got != size {
		t.Fatalf("NewPool started %d workers, want %d", got, size)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestNewPoolAggregatesStartupErrors covers the failure path: if a worker
// fails to start, NewPool must still return an error (not hang, not report
// success with a partially-started pool).
func TestNewPoolAggregatesStartupErrors(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "no-such-exiftool")
	if _, err := NewPool(bogus, 4); err == nil {
		t.Fatal("NewPool with a nonexistent binary = nil error, want one")
	}
}

// TestPoolExtractUsesConcurrentlyStartedWorkers is a smoke test that a
// worker started by the concurrent NewPool actually works.
func TestPoolExtractUsesConcurrentlyStartedWorkers(t *testing.T) {
	p, err := NewPool(installtest.Exiftool(t), 2)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer p.Close()

	if _, err := p.Extract(context.Background(), filepath.Join(t.TempDir(), "missing.jpg")); err == nil {
		t.Fatal("Extract on a missing file = nil error, want one")
	}
}

// writeFile makes a file named name holding content in a fresh folder.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractRejectsLineBreaksInPath(t *testing.T) {
	e, err := New(installtest.Exiftool(t))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for _, p := range []string{"/a/x.jpg\n-all=\n-overwrite_original\n/a/y.jpg", "/a/x\r.jpg"} {
		if _, err := e.Extract(context.Background(), p); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("Extract(%q) = %v, want ErrUnsafePath", p, err)
		}
	}
	if e.Dead() {
		t.Error("an unsafe path must not cost the worker")
	}
}

// A timeout far shorter than any exiftool answer stands in for a file exiftool is stuck on.
func TestExtractTimeoutKillsWorker(t *testing.T) {
	e, err := newExtractor(installtest.Exiftool(t), time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	photo := writeFile(t, "x.jpg", "bytes")
	start := time.Now()
	if _, err := e.Extract(context.Background(), photo); !errors.Is(err, ErrProcess) {
		t.Fatalf("Extract = %v, want ErrProcess", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("Extract did not return promptly after the timeout")
	}
	if !e.Dead() {
		t.Fatal("a timed-out worker must be marked dead")
	}
	if _, err := e.Extract(context.Background(), photo); !errors.Is(err, ErrProcess) {
		t.Fatalf("Extract on a dead worker = %v, want ErrProcess", err)
	}
}

func TestExtractCancelKillsWorker(t *testing.T) {
	e, err := New(installtest.Exiftool(t))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Extract(ctx, writeFile(t, "x.jpg", "bytes")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Extract = %v, want the context's error", err)
	}
}

func TestPoolReplacesDeadWorker(t *testing.T) {
	p, err := newPool(installtest.Exiftool(t), 1, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	photo := writeFile(t, "x.jpg", "bytes")
	p.Extract(context.Background(), photo) // the timeout kills the only worker
	first := <-p.workers
	p.workers <- first
	if !first.Dead() {
		t.Fatal("setup: worker should be dead")
	}
	p.Extract(context.Background(), photo)
	second := <-p.workers
	p.workers <- second
	if second == first {
		t.Fatal("the pool handed out a dead worker instead of starting a new one")
	}
}

// exiftool prints nothing on stdout for a missing file: unknown tags, not
// empty ones, and not a dead worker.
func TestExtractWithoutOutputIsUnreadable(t *testing.T) {
	e, err := New(installtest.Exiftool(t))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	_, err = e.Extract(context.Background(), filepath.Join(t.TempDir(), "missing.jpg"))
	if !errors.Is(err, ErrUnreadable) || errors.Is(err, ErrProcess) {
		t.Fatalf("Extract = %v, want ErrUnreadable and not ErrProcess", err)
	}
	if e.Dead() {
		t.Error("a file exiftool could not open must not cost the worker")
	}
}

// An exiftool I/O error is an unreadable file (retried later); a format it
// doesn't understand is a readable file with no tags.
func TestExtractClassifiesExiftoolErrors(t *testing.T) {
	for _, tt := range []struct {
		name           string
		file           func(t *testing.T) string
		wantUnreadable bool
	}{
		{"cannot open", func(t *testing.T) string {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				t.Skip("Windows and root read a file whatever its mode")
			}
			p := writeFile(t, "x.jpg", "bytes")
			if err := os.Chmod(p, 0); err != nil {
				t.Fatal(err)
			}
			return p
		}, true},
		{"unknown format", func(t *testing.T) string { return writeFile(t, "x.xyz", "\x00\x01\x02junk") }, false},
		{"no error", func(t *testing.T) string { return writeFile(t, "x.jpg", "bytes") }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.file(t)
			e, err := New(installtest.Exiftool(t))
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			_, err = e.Extract(context.Background(), path)
			if got := errors.Is(err, ErrUnreadable); got != tt.wantUnreadable {
				t.Errorf("Extract = %v; unreadable = %v, want %v", err, got, tt.wantUnreadable)
			}
			if !tt.wantUnreadable && err != nil {
				t.Errorf("Extract = %v, want the (empty) metadata", err)
			}
		})
	}
}
