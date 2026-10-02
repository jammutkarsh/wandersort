package install

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Before anything is ready, LocationNow reports ErrPending without blocking,
// and Close on a never-started Coordinator returns at once.
func TestReadinessIsNotReadyUntilClosed(t *testing.T) {
	c := New(Options{})

	if _, err := c.LocationNow(); !errors.Is(err, ErrPending) {
		t.Errorf("LocationNow() = %v before Start, want ErrPending", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close() before Start = %v, want nil", err)
	}
}

// TestGettersUnblockOnceReady covers the happens-before contract the whole
// package exists for: once locReady/exifReady close, the blocking getters
// return the values written before the close, from a different goroutine,
// with no data race (run with -race).
func TestGettersUnblockOnceReady(t *testing.T) {
	c := New(Options{})

	go func() {
		c.exifPath, c.exifErr = "/bin/exiftool", nil
		close(c.exifReady)
		c.resolver, c.locErr = nil, nil
		close(c.locReady)
	}()

	path, err := c.Exiftool(context.Background())
	if err != nil || path != "/bin/exiftool" {
		t.Errorf("Exiftool() = %q, %v, want /bin/exiftool, nil", path, err)
	}
	// Location() blocks until locReady closes, establishing the
	// happens-before edge the LocationNow() check below relies on.
	if _, err := c.Location(context.Background()); err != nil {
		t.Errorf("Location() = %v, want nil", err)
	}
	if _, err := c.LocationNow(); err != nil {
		t.Errorf("LocationNow() = %v after locReady closed, want nil", err)
	}
}

func TestDownloadVerifiesChecksum(t *testing.T) {
	body := []byte("wandersort dependency payload")
	goodSum := fmt.Sprintf("%x", sha256.Sum256(body))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	tests := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{"no digest requested", "", false},
		{"correct digest", goodSum, false},
		{"wrong digest", "0000000000000000000000000000000000000000000000000000000000000000", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "payload.bin")

			err := downloadFile(context.Background(), dest, srv.URL, tt.want, nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Download error = %v, wantErr %v", err, tt.wantErr)
			}

			got, statErr := os.ReadFile(dest)
			if tt.wantErr {
				// a rejected download must not leave the bad file behind
				if statErr == nil {
					t.Errorf("dest still exists after a checksum mismatch")
				}
				return
			}
			if statErr != nil {
				t.Fatalf("dest missing after a successful download: %v", statErr)
			}
			if string(got) != string(body) {
				t.Errorf("dest = %q, want %q", got, body)
			}
		})
	}
}

func TestTryUpTo(t *testing.T) {
	errNet := errors.New("connection reset")
	errQuit := errors.New("user quit")
	tests := []struct {
		name      string
		failFirst int   // tries that fail before one succeeds
		stopAt    int   // BeforeRetry refuses before this try (0 = never)
		wantTries int   // tries run
		wantAsked []int // tries BeforeRetry was asked about
		wantErr   error
	}{
		{"first try works", 0, 0, 1, nil, nil},
		{"second try works", 1, 0, 2, []int{2}, nil},
		{"third try works", 2, 0, 3, []int{2, 3}, nil},
		{"gives up after three", 5, 0, 3, []int{2, 3}, errNet},
		{"user stops the retry", 5, 2, 1, []int{2}, errQuit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tries := 0
			var asked []int
			err := tryUpTo(context.Background(), MaxTries,
				func(_ context.Context, next int, prev error) error {
					asked = append(asked, next)
					if !errors.Is(prev, errNet) {
						t.Errorf("BeforeRetry got %v, want the previous try's error", prev)
					}
					if next == tt.stopAt {
						return errQuit
					}
					return nil
				},
				func(context.Context) error {
					tries++
					if tries <= tt.failFirst {
						return errNet
					}
					return nil
				})
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Errorf("tryUpTo() = %v, want %v", err, tt.wantErr)
			}
			if tries != tt.wantTries {
				t.Errorf("ran %d tries, want %d", tries, tt.wantTries)
			}
			if !slices.Equal(asked, tt.wantAsked) {
				t.Errorf("BeforeRetry asked %v, want %v", asked, tt.wantAsked)
			}
		})
	}
}

// A cancelled context ends the tries at once, without asking to retry.
func TestTryUpToStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tries := 0
	err := tryUpTo(ctx, MaxTries,
		func(context.Context, int, error) error { t.Error("BeforeRetry called after cancel"); return nil },
		func(context.Context) error { tries++; cancel(); return context.Canceled })
	if !errors.Is(err, context.Canceled) || tries != 1 {
		t.Errorf("tryUpTo() = %v after %d tries, want context.Canceled after 1", err, tries)
	}
}

// TestDownloadCleansStaleTempFiles covers the leftover-.dl-* report: a
// process killed mid-download (SIGKILL, panic) never runs the defer that
// cleans up its temp file. The next download into the same directory must
// sweep it, even though the random suffix means the exact name differs
// every time.
func TestDownloadCleansStaleTempFiles(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, ".dl-leftover-12345")
	if err := os.WriteFile(stale, []byte("orphaned"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := []byte("wandersort dependency payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(dir, "payload.bin")
	if err := downloadFile(context.Background(), dest, srv.URL, "", nil); err != nil {
		t.Fatalf("downloadFile() = %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale temp file still exists after download, want it swept")
	}
}

func TestFailedListsEveryDependency(t *testing.T) {
	exif := &DependencyError{Phase: PhaseExiftool, Err: errors.New("refused")}
	loc := &DependencyError{Phase: PhaseLocation, Err: errors.New("refused")}
	err := fmt.Errorf("gave up after 3 tries: %w", errors.Join(exif, loc))
	got := Failed(err)
	if len(got) != 2 || got[0] != exif || got[1] != loc {
		t.Errorf("Failed() = %v, want both dependencies in install order", got)
	}
	if Failed(errors.New("lock")) != nil {
		t.Error("an error naming no dependency lists none")
	}
}
