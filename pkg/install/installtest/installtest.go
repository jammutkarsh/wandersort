// Package installtest gives tests the real location.db and exiftool through
// the app's own setup path.
package installtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Resolver returns a Resolver backed by the real geonames database,
// downloading it once per machine if missing.
func Resolver(t testing.TB) *location.Resolver {
	t.Helper()
	dbPath := filepath.Join(depsDir(t), config.LocationDBFileName)

	// a failed open here means the download couldn't happen (offline) — skip
	// rather than fail, since that's not a defect in the code under test
	resolver, locationDB, err := install.OpenLocationResolver(context.Background(), logger.NewNoopLogger(), dbPath, nil)
	if err != nil {
		t.Skipf("location.db unavailable (offline?): %v", err)
	}
	t.Cleanup(func() { locationDB.Close() })
	return resolver
}

// Exiftool returns the real exiftool the app installs, installing it once per
// machine if missing.
func Exiftool(t testing.TB) string {
	t.Helper()
	dir := depsDir(t)
	c := install.New(install.Options{
		ExecutablePath: filepath.Join(dir, "bin"),
		LocationDBPath: filepath.Join(dir, config.LocationDBFileName),
		Log:            logger.NewNoopLogger(),
	})
	c.Start(context.Background())
	t.Cleanup(func() { c.Close() })
	path, err := c.Exiftool(context.Background())
	if err != nil {
		t.Skipf("exiftool unavailable (offline?): %v", err)
	}
	return path
}

// depsDir is where test dependencies live: WANDERSORT_TEST_DEPS_DIR (set by
// `make test`, prefetched with progress), else the app's ~/.wandersort cache.
func depsDir(t testing.TB) string {
	t.Helper()
	if dir := os.Getenv("WANDERSORT_TEST_DEPS_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	return filepath.Join(home, ".wandersort")
}
