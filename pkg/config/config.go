// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/lock"
)

// DefaultLibrary is the output folder's name under $HOME until the user picks
// one; the settings wizard shows it as the placeholder.
const DefaultLibrary = "WandersortLibrary"

const (
	defaultDBFileName  = ".wandersort.db"
	locationDBFileName = "location.db"
	defaultLogLevel    = "info"
)

// Configuration is one run's resolved settings: the library's own settings
// (Settings, read from its database by the caller once the library is open)
// plus the paths and counts this process computes for itself.
type Configuration struct {
	Settings

	// appDir is ~/.wandersort: the location database, the logs, and the
	// library history — everything that is about this machine rather than
	// about one library.
	appDir string

	// Workers is not a setting. It sizes the goroutine and exiftool pools,
	// both CPU-bound, so the CPU's own number is the right one — while the
	// only disk-bound thing in the pipeline (the metadata phase's byte reads)
	// is throttled by the storage class instead, in pkg/core/metadata. A
	// hand-set count could only make one of those two worse.
	Workers        int
	AppDBPath      string
	LocationDBPath string
	LogLevel       string
	LogConsole     bool
	// LogDir holds the logs of runs worth keeping (logger.NewFile, written
	// once Persist is called), apart from any library: a log is about a run,
	// not about the folder it wrote to.
	LogDir         string
	ExecutablePath string
}

// New builds the runtime configuration. The library's own settings start at
// their defaults — they are read from the library's database once it is open
// (see LoadSettings), since a library's rules travel with the library.
//
// The output folder is the most recently used one that still exists, so
// `wandersort organise` on a later launch opens the library the last scan
// filled; a caller with an explicit folder passes it to SetOutput.
func New() (*Configuration, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home directory: %w", err)
	}

	appDir := filepath.Join(home, ".wandersort")
	cfg := &Configuration{
		Settings:       DefaultSettings(),
		appDir:         appDir,
		LocationDBPath: filepath.Join(appDir, locationDBFileName),
		LogLevel:       defaultLogLevel,
		LogConsole:     true,
		LogDir:         filepath.Join(appDir, "logs"),
		Workers:        runtime.NumCPU(),
		ExecutablePath: filepath.Join(appDir, "bin"),
	}

	out := filepath.Join(home, DefaultLibrary)
	if recent := cfg.History(); len(recent) > 0 {
		out = recent[0]
	}
	cfg.SetOutput(out)
	return cfg, nil
}

// SetOutput points the configuration at an output folder. Callable only
// before the library is open: the database and the lock are already on the
// old folder after that, and one library's folder never moves (spec D5).
func (cfg *Configuration) SetOutput(dir string) {
	cfg.AppDBPath = filepath.Join(dir, defaultDBFileName)
}

// OutputDir is the library folder this run works in.
func (cfg *Configuration) OutputDir() string { return filepath.Dir(cfg.AppDBPath) }

// osClutter is what an OS drops into any folder it has shown; a folder holding
// only these is still empty as far as the user is concerned.
var osClutter = map[string]bool{".DS_Store": true, "Thumbs.db": true, "desktop.ini": true}

// CheckLibrary reports whether dir may be used as an output folder: it must
// not exist yet, be empty, or already be a library (hold .wandersort.db).
// Planning only knows files the database placed, so anything else — a folder
// organized by hand, or a library whose database was deleted — would receive
// files on top of content nothing accounts for. OS clutter doesn't count, nor
// does a lone lock file: it is taken just before the database is created, so
// a run that died in between leaves one behind.
func CheckLibrary(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("output folder %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.Name() == defaultDBFileName {
			return nil
		}
	}
	for _, e := range entries {
		if e.Name() == db.BackupFileName {
			return fmt.Errorf("output folder %s has a database backup (%s) but no database; run 'wandersort admin db --restore' to restore it", dir, db.BackupFileName)
		}
	}
	for _, e := range entries {
		if n := e.Name(); !osClutter[n] && n != lock.OutputFileName {
			return fmt.Errorf("output folder %s already has files (%s) and is not a WanderSort library; pick an empty folder or an existing library", dir, n)
		}
	}
	return nil
}
