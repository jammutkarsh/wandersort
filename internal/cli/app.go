// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jmoiron/sqlx"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Execute runs the wandersort CLI. It is this package's only exported symbol:
// the app and everything hanging off it stay internal, so main.go has one way
// in and no state to assemble. a.Config starts nil — PersistentPreRunE builds
// it via config.Resolve before any command body runs, so nothing here reads
// it early.
func Execute() error {
	a := &app{}
	return a.newRootCmd().Execute()
}

type app struct {
	Config *config.Configuration
	Log    logger.Logger
	AppDB  *db.DB
	// Deps coordinates the two downloadable dependencies (exiftool, the
	// location database) for the current command. Built once per command via
	// newDeps; nil until then. See pkg/install — this is the one place
	// exiftool path / location resolver readiness live, replacing what used
	// to be raw goroutines writing directly into *app fields.
	Deps *install.Coordinator
}

// newDeps builds a Coordinator wired to this app's config and log.
// onProgress may be nil (every non-TUI path).
func (a *app) newDeps(onProgress func(phase string, done, total int64)) *install.Coordinator {
	return install.New(install.Options{
		ExecutablePath: a.Config.ExecutablePath,
		LocationDBPath: a.Config.LocationDBPath,
		Log:            a.Log,
		OnProgress:     onProgress,
	})
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

func (a *app) closeDBs() {
	a.Log.Info("Closing databases")
	// A failed Close can leave the WAL/SHM files locked (locking_mode=EXCLUSIVE),
	// preventing the next scan from starting — always log the cause.
	if a.AppDB != nil {
		if err := a.AppDB.Close(); err != nil {
			a.Log.Error("failed to close app database", "error", err)
		}
	}
	if a.Deps != nil {
		if ldb := a.Deps.LocationDBIfReady(); ldb != nil {
			if err := ldb.Close(); err != nil {
				a.Log.Error("failed to close location database", "error", err)
			}
		}
	}
}

// tuiEnabled decides whether a command renders the full-screen TUI or falls
// back to plain line logging. Plain when --plain is set or stderr isn't a
// terminal (piped/redirected). The TUI draws to stderr, matching the review
// TUI, so stdout stays clean for piping.
func (a *app) tuiEnabled(cmd *cobra.Command) bool {
	if plain, _ := cmd.Flags().GetBool(flagPlain); plain {
		return false
	}
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// syncAnchors reads the globally-saved home/work towns and ensures they exist
// as ANCHOR_HOME/ANCHOR_WORK user_labels in this library's DB. Anchors are a
// global setting, but resolveLocations reads them per-library, so each
// library's DB needs its own copy. Idempotent and silent once synced; empty
// names are a no-op. A config file it can't read is a warning, not a failure —
// anchors only sharpen the proposal, they don't gate it.
func (a *app) syncAnchors(ctx context.Context, resolver *location.Resolver) error {
	if resolver == nil {
		return nil
	}
	g, err := config.LoadGlobal()
	if err != nil {
		a.Log.Warn("Could not read global config, skipping anchor sync", "error", err)
		return nil
	}

	for _, anchor := range []struct{ name, kind string }{
		{g.HomeWork.Home, "ANCHOR_HOME"},
		{g.HomeWork.Work, "ANCHOR_WORK"},
	} {
		if anchor.name == "" {
			continue
		}
		var exists int
		if err := a.AppDB.SQL.GetContext(ctx, &exists,
			`SELECT COUNT(*) FROM user_labels WHERE kind = ? AND label = ?`, anchor.kind, anchor.name); err != nil {
			return fmt.Errorf("check anchor %q: %w", anchor.name, err)
		}
		if exists > 0 {
			continue
		}
		lat, lon, err := resolver.ResolveByName(ctx, anchor.name)
		if err != nil {
			a.Log.Warn("Could not resolve saved anchor town", "town", anchor.name, "error", err)
			continue
		}
		if !a.AppDB.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO user_labels (label, kind, gps_lat, gps_lon) VALUES (?, ?, ?, ?)`,
				anchor.name, anchor.kind, lat, lon)
			return err
		}) {
			return fmt.Errorf("save anchor %q: writer closed", anchor.name)
		}
		a.Log.Info("Synced anchor for this library", logger.UserKey, true, "town", anchor.name, "kind", anchor.kind)
	}
	return nil
}
