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
)

const (
	DefaultLibraryDir  = "WanderSortLibrary"
	DefaultLogFileName = ".wandersort.log"
	DefaultDBFileName  = ".wandersort.db"
	locationDBFileName = "location.db"
	defaultLogLevel    = "info"
	defaultPort        = "7658"
)

type Configuration struct {
	ServerPort     string
	AppDBPath      string
	LocationDBPath string
	LogLevel       string
	LogConsole     bool
	LogFile        string
	Workers        int
	ExecutablePath string
	// Rules overrides vfs.DefaultConfig's Rules when non-empty; "none"
	// means no levels at all (flat Year/Month). Validated against
	// vfs.Rules* in cli.
	Rules []string
	// CollapseLevels drops a device/orientation/media folder level that has
	// only one value across the whole library — see vfs.Config.CollapseLevels.
	CollapseLevels bool
	// HomeWorkDateOnly groups photos taken at a home/work place by date only,
	// not location — see vfs.Config.HomeWorkDateOnly.
	HomeWorkDateOnly bool
	// MergeSameLocationDays collapses consecutive same-location day folders into
	// a dated range folder — see vfs.Config.MergeSameLocationDays.
	MergeSameLocationDays bool
}

// Defaults returns a Configuration populated with hardcoded defaults only.
// No environment variables are read. Use for CLI where ENV > CLI > defaults predecence applies.
func Defaults() (*Configuration, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home directory: %w", err)
	}

	appDir := filepath.Join(home, ".wandersort")
	executablesDirectory := filepath.Join(appDir, "bin")
	outputPath := filepath.Join(home, DefaultLibraryDir)

	return &Configuration{
		ServerPort:            defaultPort,
		AppDBPath:             filepath.Join(outputPath, DefaultDBFileName),
		LocationDBPath:        filepath.Join(appDir, locationDBFileName),
		LogLevel:              defaultLogLevel,
		LogConsole:            true,
		LogFile:               filepath.Join(outputPath, DefaultLogFileName),
		Workers:               runtime.NumCPU(),
		ExecutablePath:        executablesDirectory,
		CollapseLevels:        true,
		HomeWorkDateOnly:      true,
		MergeSameLocationDays: true,
	}, nil
}
