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
	"strconv"
)

const (
	defaultLibraryDir  = "WanderSortLibrary"
	defaultLogFileName = ".wandersort.log"
	defaultDBFileName  = ".wandersort.db"
	locationDBFileName = "location.db"
	defaultLogLevel    = "info"
	defaultPort        = "8080"
)

type Configuration struct {
	ServerPort     string
	Host           string
	AppDBPath      string
	LocationDBPath string
	LogLevel       string
	LogConsole     bool
	LogFile        string
	Workers        int
	BinDir         string
}

// Load reads environment variables and builds the application Configuration
// Defaults: info log level, port 8080, WanderSortLibrary output dir, and runtime.NumCPU() workers
func Load() (*Configuration, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home directory: %w", err)
	}

	// appDir is WanderSort's per-user configuration directory
	appDir := filepath.Join(home, ".wandersort")

	// binDir stores downloaded helper executables such as exiftool
	binDir := filepath.Join(appDir, "bin")

	outputPath := os.Getenv("OUTPUT_PATH")
	if outputPath == "" {
		outputPath = filepath.Join(home, defaultLibraryDir)
	}

	logPath := filepath.Join(outputPath, defaultLogFileName)
	dbPath := filepath.Join(outputPath, defaultDBFileName)
	dbLocationPath := filepath.Join(appDir, locationDBFileName)

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = defaultLogLevel
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	workerCount := runtime.NumCPU()
	if workersEnv := os.Getenv("WORKERS"); workersEnv != "" {
		if workers, err := strconv.Atoi(workersEnv); err == nil && workers > 0 {
			workerCount = workers
		}
	}

	return &Configuration{
		ServerPort:     port,
		Host:           "localhost",
		AppDBPath:      dbPath,
		LocationDBPath: dbLocationPath,
		LogLevel:       logLevel,
		LogConsole:     true,
		LogFile:        logPath,
		Workers:        workerCount,
		BinDir:         binDir,
	}, nil
}
