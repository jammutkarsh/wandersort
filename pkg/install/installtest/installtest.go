// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package installtest opens the real location.db for tests, downloading it
// first if this machine doesn't have it yet — the same install path
// (install.OpenLocationResolver) the app itself uses, so a test exercising a
// Resolver exercises the app's exact setup, not a hand-fabricated fixture.
// Lives under pkg/install, not pkg/location, because pkg/install is the one
// place that knows a downloadable dependency's version, download location,
// and readiness — pkg/location itself never downloads anything.
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

// Resolver returns a Resolver backed by the real gazetteer at
// ~/.wandersort/location.db, downloading it first if this machine doesn't
// have it yet (once per machine, not once per test run — the same path the
// app itself uses). location.db opens `mode=ro`, so a concurrent wandersort
// process never blocks this; the only reason to open fails is a download
// that couldn't happen (offline), which is what this skips on — not a
// failure of the code under test.
func Resolver(t testing.TB) *location.Resolver {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	dbPath := filepath.Join(home, ".wandersort", install.LocationDBFileName)

	resolver, locationDB, err := install.OpenLocationResolver(context.Background(), logger.NewNoopLogger(), dbPath, nil)
	if err != nil {
		t.Skipf("location.db unavailable (offline?): %v", err)
	}
	t.Cleanup(func() { locationDB.Close() })
	return resolver
}
