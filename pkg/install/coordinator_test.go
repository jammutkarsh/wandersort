package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// TestStartOfflineHappyPath drives Coordinator.Start end to end with
// everything already on disk (the real exiftool, a valid location db + meta),
// so neither dependency ever hits the network. This is the one place
// install.go's own goroutine orchestration (lock -> exiftool -> location, in
// that order) gets exercised together rather than through its individual pieces.
func TestStartOfflineHappyPath(t *testing.T) {
	dir := t.TempDir()
	exiftoolDir := realExiftoolDir(t)

	dbPath := filepath.Join(dir, config.LocationDBFileName)
	buildLocationDB(t, dbPath, 2)
	writeLocationMeta(t, dir, fileSHA256Helper(t, dbPath), 2)

	c := New(Options{ExecutablePath: exiftoolDir, LocationDBPath: dbPath, Log: logger.NewNoopLogger()})
	c.Start(context.Background())

	path, err := c.Exiftool(context.Background())
	if err != nil {
		t.Fatalf("Exiftool() error = %v", err)
	}
	if want := filepath.Join(exiftoolDir, exiftoolBin()); path != want {
		t.Errorf("Exiftool() = %q, want %q", path, want)
	}

	resolver, err := c.Location(context.Background())
	if err != nil {
		t.Fatalf("Location() error = %v", err)
	}
	if resolver == nil {
		t.Error("Location() resolver = nil, want non-nil")
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close() = %v, want the location database closed cleanly", err)
	}
}

// A database that never verifies fails every try, BeforeRetry runs before tries 2 and 3, both getters report it.
func TestStartGivesUpOnBadLocationDB(t *testing.T) {
	dir := t.TempDir()
	exiftoolDir := realExiftoolDir(t)
	dbPath := filepath.Join(dir, config.LocationDBFileName)
	buildLocationDB(t, dbPath, 1)
	writeLocationMeta(t, dir, fileSHA256Helper(t, dbPath), 999) // row count mismatch

	var asked []int
	c := New(Options{
		ExecutablePath: exiftoolDir, LocationDBPath: dbPath, Log: logger.NewNoopLogger(),
		BeforeRetry: func(_ context.Context, next int, err error) error {
			asked = append(asked, next)
			// the failed database was removed; put the bad one back so every try fails
			buildLocationDB(t, dbPath, 1)
			writeLocationMeta(t, dir, fileSHA256Helper(t, dbPath), 999)
			return nil
		},
	})
	c.Start(context.Background())

	_, err := c.Location(context.Background())
	var de *DependencyError
	if !errors.As(err, &de) || de.Phase != PhaseLocation {
		t.Fatalf("Location() = %v, want a location DependencyError", err)
	}
	if path, err := c.Exiftool(context.Background()); err != nil || path == "" {
		t.Errorf("Exiftool() = %q, %v, want the installed path: it succeeded on try 1", path, err)
	}
	if want := []int{2, 3}; !slices.Equal(asked, want) {
		t.Errorf("BeforeRetry asked before tries %v, want %v", asked, want)
	}
}

// Tools bundled in a read-only folder are used where they are: nothing is
// written beside them, not even the install lock.
func TestStartBundledWritesNothing(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, config.LocationDBFileName)
	buildLocationDB(t, dbPath, 2)
	writeLocationMeta(t, dir, fileSHA256Helper(t, dbPath), 2)
	if runtime.GOOS != "windows" { // a read-only folder there still takes new files
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
	}

	c := New(Options{ExecutablePath: realExiftoolDir(t), LocationDBPath: dbPath, Log: logger.NewNoopLogger(), Bundled: true})
	c.Start(context.Background())
	if _, err := c.Exiftool(context.Background()); err != nil {
		t.Errorf("Exiftool() = %v, want the bundled one", err)
	}
	if _, err := c.Location(context.Background()); err != nil {
		t.Errorf("Location() = %v, want the bundled one", err)
	}
	if err := c.Close(); err != nil {
		t.Error(err)
	}
}

// A bundled location database that fails its check is reported at once and
// kept: removing it would break the read-only folder it ships in, and no
// download replaces it.
func TestStartBundledKeepsAFailingDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, config.LocationDBFileName)
	buildLocationDB(t, dbPath, 1)
	writeLocationMeta(t, dir, fileSHA256Helper(t, dbPath), 999) // row count mismatch

	retried := false
	c := New(Options{
		ExecutablePath: realExiftoolDir(t), LocationDBPath: dbPath, Log: logger.NewNoopLogger(), Bundled: true,
		BeforeRetry: func(context.Context, int, error) error { retried = true; return errors.New("stop") },
	})
	c.Start(context.Background())
	_, err := c.Location(context.Background())
	if de, ok := errors.AsType[*DependencyError](err); !ok || de.Phase != PhaseLocation {
		t.Fatalf("Location() = %v, want a location DependencyError", err)
	}
	if retried {
		t.Error("a bundled database was retried")
	}
	for _, p := range []string{dbPath, filepath.Join(dir, LocationMetaFileName)} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", filepath.Base(p), err)
		}
	}
}
