// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package locationtest opens the real location.db for tests, so tests
// exercising geocoding run against the same data and the same open-verify
// path (install.OpenLocationResolver) that the app uses, instead of a
// hand-fabricated fixture. It does not download the database — a missing
// database is a test failure, not a skip.
package locationtest

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
// ~/.wandersort/location.db (the same path the app itself uses). Fails the
// test if the database is missing (run `wandersort config` once to install
// it) or can't be opened, e.g. locked by another wandersort process (it
// opens locking_mode=EXCLUSIVE).
func Resolver(t testing.TB) *location.Resolver {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("no home dir: %v", err)
	}
	dbPath := filepath.Join(home, ".wandersort", install.LocationDBFileName)

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("location database not found at %s — run 'wandersort config' once to install it", dbPath)
	}

	resolver, locationDB, err := install.OpenLocationResolver(context.Background(), logger.NewNoopLogger(), dbPath, nil)
	if err != nil {
		t.Fatalf("location.db unavailable (locked by another wandersort process?): %v", err)
	}
	t.Cleanup(func() { locationDB.Close() })
	return resolver
}
