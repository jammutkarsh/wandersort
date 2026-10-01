package install

import (
	"context"
	"errors"
	"path/filepath"
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

// TestStartLocationOnlySkipsExiftool: the exiftool getter returns at once with
// an error (nothing installs it) while the location getter goes through the
// real download/verify path.
func TestStartLocationOnlySkipsExiftool(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, config.LocationDBFileName)
	buildLocationDB(t, dbPath, 1)
	writeLocationMeta(t, dir, fileSHA256Helper(t, dbPath), 1)

	c := New(Options{LocationDBPath: dbPath, Log: logger.NewNoopLogger()})

	done := make(chan error, 1)
	c.StartLocationOnly(context.Background(), func(err error) { done <- err })

	if path, err := c.Exiftool(context.Background()); path != "" || !errors.Is(err, errExiftoolNotInstalled) {
		t.Errorf("Exiftool() after StartLocationOnly = %q, %v, want errExiftoolNotInstalled", path, err)
	}

	if err := <-done; err != nil {
		t.Errorf("StartLocationOnly onReady callback error = %v, want nil", err)
	}
	if _, err := c.Location(context.Background()); err != nil {
		t.Errorf("Location() error = %v, want nil", err)
	}
}

// TestStartLocationOnlyReportsVerifyFailure covers the onReady callback's
// error path, not just its success path above.
func TestStartLocationOnlyReportsVerifyFailure(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, config.LocationDBFileName)
	buildLocationDB(t, dbPath, 1)
	writeLocationMeta(t, dir, fileSHA256Helper(t, dbPath), 999) // row count mismatch

	c := New(Options{LocationDBPath: dbPath, Log: logger.NewNoopLogger()})
	done := make(chan error, 1)
	c.StartLocationOnly(context.Background(), func(err error) { done <- err })

	if err := <-done; err == nil {
		t.Error("onReady callback error = nil, want the verify failure")
	}
	if _, err := c.Location(context.Background()); err == nil {
		t.Error("Location() error = nil, want the verify failure")
	}
}
