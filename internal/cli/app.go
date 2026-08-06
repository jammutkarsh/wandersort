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
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type app struct {
	Config *config.Configuration
	Log    logger.Logger
	AppDB  *db.DB
	Deps   *install.Coordinator
	// outLock is the output-dir lock the shell takes lazily (see ensureOutput);
	// the subcommands hold their own and unlock it themselves.
	outLock *lock.Lock
	// overrides is the flag layer this invocation resolved with, kept so a
	// re-resolve mid-session layers the same way (see reloadConfig).
	overrides config.Overrides
}

// reloadConfig re-resolves the settings after the wizard rewrote config.yaml
// under a running session. The output path is the one setting that cannot move
// mid-session — the database and the lock are already open on the old one — so
// it is held back and returned as a note for the user instead.
func (a *app) reloadConfig() (note string, err error) {
	cfg, warning, err := config.Resolve(a.overrides)
	if err != nil {
		return "", fmt.Errorf("reload settings: %w", err)
	}
	if warning != "" {
		a.Log.Warn(warning, logger.UserKey, true)
	}
	if a.AppDB != nil && cfg.AppDBPath != a.Config.AppDBPath {
		cfg.AppDBPath, cfg.LogFile = a.Config.AppDBPath, a.Config.LogFile
		note = "Output folder saved — it takes effect the next time you start wandersort"
	}
	a.Config = cfg
	return note, nil
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

// workflowDeps gates each pipeline phase on its own dependency. Read through
// closures, not method values: the TUI path builds the workflow before the
// Coordinator exists, so a.Deps has to be resolved when a phase actually asks.
func (a *app) workflowDeps() workflow.Deps {
	return workflow.Deps{
		Exiftool: func() (string, error) {
			return a.Deps.Exiftool()
		},
		Location: func() (*location.Resolver, error) {
			return a.Deps.Location()
		},
	}
}

// lockOutput takes the exclusive output-dir lock every command that touches
// the database needs. A lock held by another process is the one error users
// hit routinely, so it gets the full styled explanation rather than a wrapped
// one-liner; pkg/lock reports the fact, this decides how it reads.
func (a *app) lockOutput() (*lock.Lock, error) {
	l, err := lock.AcquireOutput(filepath.Dir(a.Config.LogFile))
	var running *lock.AlreadyRunningError
	if errors.As(err, &running) {
		return nil, fmt.Errorf("%s", tui.Bad.Render(fmt.Sprintf("Another wandersort process is already running (PID %d).", running.PID))+"\n\n"+
			tui.FaintTxt.Render("Only one scan or review can use the same output directory at a time.")+"\n"+
			tui.FaintTxt.Render("Stop the other process, then try again."))
	}
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	return l, nil
}

// hasProposal reports whether an earlier run left something to review. The
// database file is the proxy — nothing else writes one, and answering it for
// real means opening the database, which is exactly what the caller is
// deciding whether to do.
func (a *app) hasProposal() bool {
	_, err := os.Stat(a.Config.AppDBPath)
	return err == nil
}

// ensureOutput takes the output lock and opens the database, once — the shell
// defers both until a scan or a review actually needs them, so the settings
// wizard (which only writes config.yaml) runs with nothing held and a second
// wandersort is only refused at the point it would really collide.
func (a *app) ensureOutput(ctx context.Context) error {
	if a.outLock != nil {
		return nil
	}
	l, err := a.lockOutput()
	if err != nil {
		return err
	}
	if err := a.initAppDB(ctx); err != nil {
		l.Unlock()
		return err
	}
	a.outLock = l
	return nil
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

func (a *app) isTuiEnabled(cmd *cobra.Command) bool {
	if plain, _ := cmd.Flags().GetBool(flagPlain); plain {
		return false
	}
	return term.IsTerminal(int(os.Stderr.Fd()))
}
