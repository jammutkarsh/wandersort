// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package installtest opens the real location.db for tests via
// install.OpenLocationResolver — the app's exact setup path, not a
// hand-fabricated fixture. Lives under pkg/install since that's the one
// package that knows a downloadable dependency's version/location/readiness.
package installtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Resolver returns a Resolver backed by the real geonames database,
// downloading it once per machine if missing.
func Resolver(t testing.TB) *location.Resolver {
	t.Helper()
	dbPath := filepath.Join(depsDir(t), install.LocationDBFileName)

	// a failed open here means the download couldn't happen (offline) — skip
	// rather than fail, since that's not a defect in the code under test
	resolver, locationDB, err := install.OpenLocationResolver(context.Background(), logger.NewNoopLogger(), dbPath, nil)
	if err != nil {
		t.Skipf("location.db unavailable (offline?): %v", err)
	}
	t.Cleanup(func() { locationDB.Close() })
	return resolver
}

// depsDir is where a downloadable dependency is expected on disk.
// WANDERSORT_TEST_DEPS_DIR, set by `make test` (which runs `make test-deps`
// first via scripts/fetchtestdeps), points at a gitignored test/deps
// directory pre-populated with visible download progress — so a plain
// `go test` never triggers install.OpenLocationResolver's own silent,
// no-progress download mid test run. Unset (running go test directly,
// outside make) falls back to the app's real ~/.wandersort cache,
// downloading on first use exactly as wandersort itself would.
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
