// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package locationtest opens the real, downloaded location.db for tests, so
// tests exercising geocoding run against the same data and the same
// download-open-verify path (location.Open) that the app uses, instead of a
// hand-fabricated fixture.
package locationtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Resolver returns a Resolver backed by the real gazetteer at
// ~/.wandersort/location.db, downloading it first if this machine doesn't
// have it yet (once per machine, not once per test run — the same path the
// app itself uses). Skips only when the download can't happen (offline) or
// the db is locked by another wandersort process (it opens
// locking_mode=EXCLUSIVE) — neither is a failure of the code under test.
func Resolver(t testing.TB) *location.Resolver {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	dbPath := filepath.Join(home, ".wandersort", location.LocationDBFileName)

	resolver, locationDB, err := location.Open(context.Background(), logger.NewNoopLogger(), dbPath, nil)
	if err != nil {
		t.Skipf("location.db unavailable (offline, or locked by another wandersort process): %v", err)
	}
	t.Cleanup(func() { locationDB.Close() })
	return resolver
}
