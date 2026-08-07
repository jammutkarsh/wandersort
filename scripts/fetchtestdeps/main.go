// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// fetchtestdeps pre-downloads exiftool and the location database into a
// gitignored local directory (test/deps by default) before `go test` runs,
// so a test needing a real dependency (pkg/install/installtest) finds it
// already on disk instead of triggering install.Coordinator's own download
// mid test run, with no progress visible in test output. Run via
// `make test-deps` (which `make test` depends on), not directly.
//
// It goes through install.Coordinator exactly as wandersort itself does —
// same download, checksum verify, and decompression path — just with a
// progress printer instead of a TUI bar.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func main() {
	dir := "test/deps"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "fetchtestdeps:", err)
		os.Exit(1)
	}
	ctx := context.Background()
	log := logger.New("info", true, "")
	dbPath := filepath.Join(dir, install.LocationDBFileName)

	// Location first, and fully awaited, before exiftool even starts: a
	// real bug in Coordinator.Start (unrelated to this tool, not fixed
	// here) runs exiftool then location sequentially, and an exiftool
	// failure closes locReady without ever attempting the location
	// download and without setting locErr — Location() then silently
	// returns (nil, nil) instead of an error. Fetching location.db to
	// completion first, on its own Coordinator, sidesteps that: by the
	// time exiftool's Coordinator (below) reaches its own location half,
	// the file already exists and that half is an instant no-op.
	loc := install.New(install.Options{
		LocationDBPath: dbPath,
		Log:            log,
		OnProgress:     printProgress,
	})
	loc.StartLocationOnly(ctx, nil)
	if _, err := loc.Location(); err != nil {
		fmt.Fprintln(os.Stderr, "fetchtestdeps: location db:", err)
		os.Exit(1)
	}

	// No test needs the real exiftool binary today, so its failure is a
	// warning, not a build-breaking error — location.db above is the one
	// every fuzzy-search test actually depends on.
	exif := install.New(install.Options{
		ExecutablePath: filepath.Join(dir, "bin"),
		LocationDBPath: dbPath,
		Log:            log,
		OnProgress:     printProgress,
	})
	exif.Start(ctx)
	if _, err := exif.Exiftool(); err != nil {
		fmt.Fprintln(os.Stderr, "fetchtestdeps: exiftool (non-fatal, no test needs it yet):", err)
	}

	fmt.Println("test deps ready:", dir)
}

// printProgress renders a single, self-overwriting line per phase — the
// thing that was missing when tests downloaded these silently.
func printProgress(phase string, done, total int64) {
	if total <= 0 {
		return
	}
	pct := float64(done) / float64(total) * 100
	fmt.Printf("\r%-10s %6.1f%%  (%d/%d bytes)", phase, pct, done, total)
	if done >= total {
		fmt.Println()
	}
}
