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

// Resolver returns a Resolver backed by the real gazetteer at
// ~/.wandersort/location.db, downloading it once per machine if missing.
func Resolver(t testing.TB) *location.Resolver {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	dbPath := filepath.Join(home, ".wandersort", install.LocationDBFileName)

	// a failed open here means the download couldn't happen (offline) — skip
	// rather than fail, since that's not a defect in the code under test
	resolver, locationDB, err := install.OpenLocationResolver(context.Background(), logger.NewNoopLogger(), dbPath, nil)
	if err != nil {
		t.Skipf("location.db unavailable (offline?): %v", err)
	}
	t.Cleanup(func() { locationDB.Close() })
	return resolver
}
