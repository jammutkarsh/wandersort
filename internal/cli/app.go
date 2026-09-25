// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
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
	// outLock is the output-dir lock, taken with the database by openLibrary
	// and released with it by closeDBs.
	outLock *lock.Lock
	// logFile is this process's log, shared by the startup logger and the
	// shell's TUI logger. It stays in memory until openLibrary (or a warning)
	// persists it, so a run that only explores the app leaves no file.
	logFile *logger.File
}

func Execute() error {
	a := &app{}
	return a.newRootCmd().Execute()
}

// interruptible is the context a plain command that touches the library runs
// under. The first ctrl+c (or SIGTERM) cancels it, so the work stops at its
// next safe point and the deferred closes — writer flush, database, output
// lock — still run. A second one gets the default behaviour back and ends
// the process, for work that won't unwind.
func interruptible() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx, stop
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
	l, err := lock.AcquireOutput(filepath.Dir(a.Config.AppDBPath))
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

// openLibrary opens the output folder as a library, once per session: check
// it may be one, take the output lock, then open (or create) the database.
// The order is the point — nothing is written into the folder before the
// lock, and nothing but the lock before the database, so a refused folder is
// never touched and a lost race (another process locked it first) creates no
// file either: the lock file it tried to open is the winner's. Every command
// and the shell open the library here, lazily, so a session that never scans
// or reviews writes nothing outside the logs.
func (a *app) openLibrary(ctx context.Context) error {
	if a.AppDB != nil {
		return nil
	}
	// From here the run touches (or tried to touch) user data: keep its log.
	a.logFile.Persist()
	outputDir := a.Config.OutputDir()
	if err := config.CheckLibrary(outputDir); err != nil {
		return err
	}
	l, err := a.lockOutput()
	if err != nil {
		return err
	}
	appDB, err := db.New(ctx, a.Config.AppDBPath, db.AppDB, a.Log)
	if err != nil {
		l.Unlock()
		return fmt.Errorf("app db: %w", err)
	}
	// The folders this library gets are the ones it was organized under, not
	// whatever another library is set to (spec D2). A library that has never
	// been through the wizard has no row and keeps the defaults. Read before
	// the handle is published: a session that carried on with a half-open
	// library would find `a.AppDB` set and skip the retry.
	settings, err := config.LoadSettings(ctx, appDB)
	if err != nil {
		appDB.Close()
		l.Unlock()
		return err
	}
	a.AppDB, a.outLock = appDB, l
	a.Config.Settings = settings
	// Only now is this folder known to really be a library, which is what
	// makes it worth offering as a recent one next launch (spec D3).
	if err := a.Config.Remember(outputDir); err != nil {
		a.Log.Warn("could not record this library as recently used", "error", err)
	}
	return nil
}

// saveSettings writes the settings the wizard collected into the library they
// belong to, opening it first if this session hasn't yet — a config-first
// session picks its output folder here, and nothing is written anywhere until
// it does.
func (a *app) saveSettings(ctx context.Context, outputDir string, s config.Settings) error {
	if a.AppDB == nil {
		a.Config.SetOutput(outputDir)
		if err := a.openLibrary(ctx); err != nil {
			return err
		}
	}
	if err := config.SaveSettings(ctx, a.AppDB, s); err != nil {
		return err
	}
	a.Config.Settings = s
	return nil
}

func (a *app) closeDBs() {
	// A failed Close can leave the WAL/SHM files locked (locking_mode=EXCLUSIVE),
	// preventing the next scan from starting — always log the cause.
	if a.AppDB != nil {
		a.Log.Info("Closing databases")
		if err := a.AppDB.Close(); err != nil {
			a.Log.Error("failed to close app database", "error", err)
		}
	}
	a.outLock.Unlock() // nil-safe; after Close, so no other process opens the database mid-close
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

// confirm asks a yes/no question before something irreversible: a themed
// dialog in the full-screen TUI, or a plain y/N prompt when --plain /
// non-interactive. Anything but an explicit yes is a no.
func (a *app) confirm(cmd *cobra.Command, title, detail string) bool {
	if !a.isTuiEnabled(cmd) {
		fmt.Fprint(os.Stderr, tui.Attn.Render(title)+" (y/N): ")
		input, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		return input == "y" || input == "yes"
	}
	ok := false
	prog := tea.NewProgram(tui.NewConfirmModel(title, detail, &ok), tea.WithAltScreen(), tea.WithOutput(os.Stderr))
	if _, err := prog.Run(); err != nil {
		return false
	}
	return ok
}
