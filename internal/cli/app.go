// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"golang.org/x/term"
)

// Execute runs the wandersort CLI. It is this package's only exported symbol:
// the app and everything hanging off it stay internal, so main.go has one way
// in and no state to assemble.
func Execute(cfg *config.Configuration) error {
	a := &app{Config: cfg}
	return a.newRootCmd().Execute()
}

type app struct {
	Config           *config.Configuration
	Log              logger.Logger
	ExiftoolPath     string
	AppDB            *db.DB
	LocationDB       *db.DB
	LocationResolver *location.Resolver
	// InstallProgress, when set, receives dependency-download byte progress
	// (phase is "exiftool"/"location") so the install screen can draw a bar.
	// Not routed through the logger — per-byte ticks would flood the file log.
	// nil in every non-TUI path.
	InstallProgress func(phase string, done, total int64)
}

// progressFor binds InstallProgress to one install phase for a Setup callback,
// or returns nil when no TUI is listening (so the download skips the wrapper).
func (a *app) progressFor(phase string) func(done, total int64) {
	if a.InstallProgress == nil {
		return nil
	}
	return func(done, total int64) { a.InstallProgress(phase, done, total) }
}

func (a *app) initAppDB(ctx context.Context) error {
	if a.AppDB != nil {
		return nil
	}
	appDB, err := db.New(ctx, a.Config.AppDBPath, db.AppDB, a.Log)
	if err != nil {
		return fmt.Errorf("app db: %w", err)
	}
	a.AppDB = appDB
	return nil
}

func (a *app) initLocationResolver(ctx context.Context) error {
	if a.LocationResolver != nil {
		return nil
	}
	// Download the location database on first use so scan/serve work without a
	// separate setup step. No-op if it already exists.
	resolver, locationDB, err := location.Open(ctx, a.Log, a.Config.LocationDBPath, a.progressFor("location"))
	if err != nil {
		return err
	}
	a.LocationDB = locationDB
	a.LocationResolver = resolver
	return nil
}

// installDir is the shared directory holding downloaded dependencies (exiftool
// binaries and the location database). It also hosts the install coordination lock.
func (a *app) installDir() string {
	return filepath.Dir(a.Config.LocationDBPath)
}

// ensureDependencies installs exiftool and the location database if missing,
// then opens the resolver. Holds the install lock throughout, so a concurrent
// scan/serve waits rather than installing at the same time.
//
// Exiftool installs first on purpose: it's the small download and the earlier
// pipeline phase (exif) is what waits on it — the location database is only
// needed by the last phase (vfs), so it downloads behind the rest of the
// pipeline (see workflow.Deps).
//
// onExiftool (may be nil) is a checkpoint: it runs
// as soon as exiftool is usable (before the location download), so a pipeline
// gating only its exif phase can proceed while the big download continues.
func (a *app) ensureDependencies(ctx context.Context, onExiftool func(error)) error {
	// try non-blocking first, so waiting can be announced rather than looking hung
	l, err := lock.AcquireInstall(ctx, a.installDir(), false)
	if errors.Is(err, lock.ErrHeld) {
		a.Log.Info("Waiting for another process to finish installing dependencies...", logger.UserKey, true)
		l, err = lock.AcquireInstall(ctx, a.installDir(), true)
	}
	if err != nil {
		if onExiftool != nil {
			onExiftool(err)
		}
		return fmt.Errorf("wait for dependency install: %w", err)
	}
	defer l.Unlock()

	exifErr := a.initExiftool(ctx)
	if onExiftool != nil {
		onExiftool(exifErr)
	}
	if exifErr != nil {
		return exifErr
	}
	return a.initLocationResolver(ctx)
}

func (a *app) initExiftool(ctx context.Context) error {
	if a.ExiftoolPath != "" {
		return nil
	}
	// Install exiftool on first use so scan/serve work without a separate setup
	// step. No-op if a suitable version is already present.
	exiftoolPath, err := exiftool.Setup(ctx, a.Log, a.Config.ExecutablePath, a.progressFor("exiftool"))
	if err != nil {
		return fmt.Errorf("exiftool: %w", err)
	}
	a.ExiftoolPath = exiftoolPath
	return nil
}

func (a *app) closeDBs() {
	a.Log.Info("Closing databases")
	// A failed Close can leave the WAL/SHM files locked (locking_mode=EXCLUSIVE),
	// preventing the next scan/serve from starting — always log the cause.
	if a.AppDB != nil {
		if err := a.AppDB.Close(); err != nil {
			a.Log.Error("failed to close app database", "error", err)
		}
	}
	if a.LocationDB != nil {
		if err := a.LocationDB.Close(); err != nil {
			a.Log.Error("failed to close location database", "error", err)
		}
	}
}

// tuiEnabled decides whether a command renders the full-screen TUI or falls
// back to plain line logging. Plain when --plain is set or stderr isn't a
// terminal (piped/redirected). The TUI draws to stderr, matching the review
// TUI, so stdout stays clean for piping.
func (a *app) tuiEnabled() bool {
	if v.GetBool(flagPlain) {
		return false
	}
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// syncAnchors reads the globally-saved home/work towns and hands them to the
// vfs phase that consumes them. A config file it can't read is a warning, not
// a failure — anchors only sharpen the proposal, they don't gate it.
func (a *app) syncAnchors(ctx context.Context) error {
	g, err := config.LoadGlobal()
	if err != nil {
		a.Log.Warn("Could not read global config, skipping anchor sync", "error", err)
		return nil
	}
	return vfs.SyncAnchors(ctx, a.AppDB, a.LocationResolver, a.Log, g.HomeWork.Home, g.HomeWork.Work)
}
