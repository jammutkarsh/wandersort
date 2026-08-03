// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// buildLocationDB writes a minimal real sqlite file at path with a
// geonames_cities table holding rows entries — standing in for the real
// (downloaded) location database, which this package only ever queries via
// database/sql, never assembles itself.
func buildLocationDB(t *testing.T, path string, rows int) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer sqlDB.Close()

	if _, err := sqlDB.Exec("CREATE TABLE geonames_cities (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := 0; i < rows; i++ {
		if _, err := sqlDB.Exec("INSERT INTO geonames_cities DEFAULT VALUES"); err != nil {
			t.Fatalf("insert row: %v", err)
		}
	}
}

func fileSHA256Helper(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func writeLocationMeta(t *testing.T, dir, hash string, rows int) {
	t.Helper()
	meta := locationMeta{Hash: hash, Rows: map[string]int{"geonames_cities": rows}}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, LocationMetaFileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyLocationDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, LocationDBFileName)
	buildLocationDB(t, dbPath, 3)
	hash := fileSHA256Helper(t, dbPath)

	openRO := func() *db.DB {
		d, err := db.New(context.Background(), dbPath, db.LocationDB, logger.NewNoopLogger())
		if err != nil {
			t.Fatalf("open location db: %v", err)
		}
		t.Cleanup(func() { d.Close() })
		return d
	}

	t.Run("checksum and row count match", func(t *testing.T) {
		writeLocationMeta(t, dir, hash, 3)
		if err := verifyLocationDB(dbPath, openRO(), logger.NewNoopLogger()); err != nil {
			t.Errorf("verifyLocationDB() = %v, want nil", err)
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		writeLocationMeta(t, dir, "0000000000000000000000000000000000000000000000000000000000000000", 3)
		if err := verifyLocationDB(dbPath, openRO(), logger.NewNoopLogger()); err == nil {
			t.Error("verifyLocationDB() = nil, want a checksum mismatch error")
		}
	})

	t.Run("row count mismatch", func(t *testing.T) {
		writeLocationMeta(t, dir, hash, 99)
		if err := verifyLocationDB(dbPath, openRO(), logger.NewNoopLogger()); err == nil {
			t.Error("verifyLocationDB() = nil, want a row count mismatch error")
		}
	})

	t.Run("missing meta file", func(t *testing.T) {
		os.Remove(filepath.Join(dir, LocationMetaFileName))
		if err := verifyLocationDB(dbPath, openRO(), logger.NewNoopLogger()); err == nil {
			t.Error("verifyLocationDB() with no meta file = nil, want an error")
		}
	})
}

// TestDownloadLocationDBSkipsExisting covers the offline-friendly branch:
// downloadLocationDB must not attempt a network request when the file is
// already on disk, which is also what keeps this test network-free.
func TestDownloadLocationDBSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, LocationDBFileName)
	buildLocationDB(t, dbPath, 1)
	before := fileSHA256Helper(t, dbPath)

	if err := downloadLocationDB(context.Background(), logger.NewNoopLogger(), dbPath, nil); err != nil {
		t.Fatalf("downloadLocationDB() = %v, want nil (file already exists)", err)
	}
	if after := fileSHA256Helper(t, dbPath); after != before {
		t.Errorf("downloadLocationDB() modified an already-present file: %s -> %s", before, after)
	}
}

// TestOpenLocationResolverOffline drives OpenLocationResolver's full
// download(skip)-open-verify path with everything already on disk, so it
// never touches the network — the download and verify halves are each
// covered independently above; this pins that the three steps compose.
func TestOpenLocationResolverOffline(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, LocationDBFileName)
	buildLocationDB(t, dbPath, 5)
	writeLocationMeta(t, dir, fileSHA256Helper(t, dbPath), 5)

	resolver, locationDB, err := OpenLocationResolver(context.Background(), logger.NewNoopLogger(), dbPath, nil)
	if err != nil {
		t.Fatalf("OpenLocationResolver() = %v, want nil", err)
	}
	defer locationDB.Close()
	if resolver == nil {
		t.Error("OpenLocationResolver() resolver = nil, want non-nil")
	}
}

// TestOpenLocationResolverVerifyFailureClosesDB covers the cleanup path: a
// failed verification must close the DB handle it just opened rather than
// leaking it back to the caller alongside the error.
func TestOpenLocationResolverVerifyFailureClosesDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, LocationDBFileName)
	buildLocationDB(t, dbPath, 5)
	writeLocationMeta(t, dir, fileSHA256Helper(t, dbPath), 999) // row count mismatch

	resolver, locationDB, err := OpenLocationResolver(context.Background(), logger.NewNoopLogger(), dbPath, nil)
	if err == nil {
		t.Fatal("OpenLocationResolver() = nil error, want a verification failure")
	}
	if resolver != nil || locationDB != nil {
		t.Errorf("OpenLocationResolver() on failure = %v, %v, want nil, nil", resolver, locationDB)
	}
}
