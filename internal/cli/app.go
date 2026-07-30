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
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jmoiron/sqlx"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type app struct {
	Config *config.Configuration
	Log    logger.Logger
	AppDB  *db.DB
	Deps   *install.Coordinator
}

func Execute() error {
	a := &app{}
	return a.newRootCmd().Execute()
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

// workflowDeps gates each pipeline phase on its own dependency and syncs
// anchors at the earliest point the resolver exists — the same wiring for the
// plain and the full-screen path. a.Deps must already be built (newDeps) and
// started (Start) before the returned Deps' closures are called.
func (a *app) workflowDeps(ctx context.Context) workflow.Deps {
	return workflow.Deps{
		Exiftool: func() (string, error) {
			return a.Deps.AwaitExiftool()
		},
		Location: func() (*location.Resolver, error) {
			resolver, err := a.Deps.AwaitLocation()
			if err != nil {
				return nil, err
			}
			// anchors need the resolver, so this is the earliest they can sync;
			// vfs is also the only phase that reads them
			if err := a.syncAnchors(ctx, resolver); err != nil {
				return nil, fmt.Errorf("anchors: %w", err)
			}
			return resolver, nil
		},
	}
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

// syncAnchors ensures the globally-saved anchor towns exist as SAVED_PLACE
// user_labels in this library's DB — a global setting, but resolveLocations
// reads it per-library, so each library needs its own copy.
func (a *app) syncAnchors(ctx context.Context, resolver *location.Resolver) error {
	if resolver == nil {
		return nil
	}
	g, err := a.Config.Load()
	if err != nil {
		// anchors only sharpen the proposal, they don't gate it
		a.Log.Warn("Could not read global config, skipping anchor sync", "error", err)
		return nil
	}

	// index 0 home, 1 work, everything after another frequently-stayed-at
	// place — all anchored the same way
	for _, name := range g.SavedPlaces {
		if name == "" {
			continue
		}
		kind := config.SavedPlace
		var exists int
		if err := a.AppDB.SQL.GetContext(ctx, &exists,
			`SELECT COUNT(*) FROM user_labels WHERE kind = ? AND label = ?`, kind, name); err != nil {
			return fmt.Errorf("check anchor %q: %w", name, err)
		}
		if exists > 0 {
			continue // already synced, idempotent
		}
		lat, lon, err := resolver.ResolveByName(ctx, name)
		if err != nil {
			a.Log.Warn("Could not resolve saved anchor town", "town", name, "error", err)
			continue
		}
		if !a.AppDB.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO user_labels (label, kind, gps_lat, gps_lon) VALUES (?, ?, ?, ?)`,
				name, kind, lat, lon)
			return err
		}) {
			return fmt.Errorf("save anchor %q: writer closed", name)
		}
		a.Log.Info("Synced anchor for this library", logger.UserKey, true, "town", name, "kind", kind)
	}
	return nil
}
