package install

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// TestStartOfflineHappyPath drives Coordinator.Start end to end with
// everything already on disk (a valid fake exiftool binary, a valid location
// db + meta), so neither dependency ever hits the network. This is the one
// place install.go's own goroutine orchestration (lock -> exiftool ->
// location, in that order) gets exercised together rather than through its
// individual pieces.
func TestStartOfflineHappyPath(t *testing.T) {
	dir := t.TempDir()
	fakeExiftool(t, filepath.Join(dir, exiftoolBin()), exiftoolVersion)

	dbPath := filepath.Join(dir, config.LocationDBFileName)
	buildLocationDB(t, dbPath, 2)
	writeLocationMeta(t, dir, fileSHA256Helper(t, dbPath), 2)

	c := New(Options{ExecutablePath: dir, LocationDBPath: dbPath, Log: logger.NewNoopLogger()})
	c.Start(context.Background())

	path, err := c.Exiftool(context.Background())
	if err != nil {
		t.Fatalf("Exiftool() error = %v", err)
	}
	if want := filepath.Join(dir, exiftoolBin()); path != want {
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

// TestStartGivesUpOnBadLocationDB: a database that never verifies fails every
// try, BeforeRetry is asked before tries 2 and 3, and both getters report it.
func TestStartGivesUpOnBadLocationDB(t *testing.T) {
	dir := t.TempDir()
	fakeExiftool(t, filepath.Join(dir, exiftoolBin()), exiftoolVersion)
	dbPath := filepath.Join(dir, config.LocationDBFileName)
	buildLocationDB(t, dbPath, 1)
	writeLocationMeta(t, dir, fileSHA256Helper(t, dbPath), 999) // row count mismatch

	var asked []int
	c := New(Options{
		ExecutablePath: dir, LocationDBPath: dbPath, Log: logger.NewNoopLogger(),
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
