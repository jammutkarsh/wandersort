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
			return a.Deps.AwaitLocation()
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
	// A failed Close can leave the WAL/SHM files locked (locking_mode=EXCLUSIVE),
	// preventing the next scan from starting — always log the cause.
	a.Log.Info("Closing databases")
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

// isTuiEnabled decides whether a command renders the full-screen TUI or falls
// back to plain line logging. Plain when --plain is set or stderr isn't a
// terminal (piped/redirected). The TUI draws to stderr, matching the review
// TUI, so stdout stays clean for piping.
func (a *app) isTuiEnabled(cmd *cobra.Command) bool {
	if plain, _ := cmd.Flags().GetBool(flagPlain); plain {
		return false
	}
	return term.IsTerminal(int(os.Stderr.Fd()))
}
