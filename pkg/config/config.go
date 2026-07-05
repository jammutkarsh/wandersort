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
	"strconv"
	"time"
)

type Configuration struct {
	ServerPort      string
	Host            string
	DatabasePath    string
	DbLocationPath  string
	OutputPath      string
	LogLevel        string
	LogConsole      bool
	LogFile         string
	Workers         int
	UpdateInterval  time.Duration
	FinalizeTimeout time.Duration
	AppDir          string
	BinDir          string
}

func Load() (*Configuration, error) {
	const defaultWorkers = 5

	var (
		outputPath         = os.Getenv("OUTPUT_PATH")
		logPath            = os.Getenv("LOG_FILE")
		logLevel           = os.Getenv("LOG_LEVEL")
		dbPath             = os.Getenv("DB_PATH")
		dbLocationPath     = os.Getenv("DB_LOCATION_PATH")
		workers            = os.Getenv("WORKERS")
		port               = os.Getenv("PORT")
		host               = os.Getenv("HOST")
		updateIntervalStr  = os.Getenv("UPDATE_INTERVAL")
		finalizeTimeoutStr = os.Getenv("FINALIZE_TIMEOUT")
		workerCount        = defaultWorkers
	)

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home directory: %w", err)
	}

	// appDir is WanderSort's per-user configuration directory.
	// Everything managed by WanderSort outside the output library
	// (downloaded tools, location database, etc.) lives here.
	appDir := filepath.Join(home, ".wandersort")

	// binDir stores downloaded helper executables such as exiftool.
	binDir := filepath.Join(appDir, "bin")

	if outputPath == "" {
		outputPath = filepath.Join(home, "WanderSortLibrary")
	}

	if logPath == "" {
		logPath = filepath.Join(outputPath, ".wandersort.log")
	}

	if logLevel == "" {
		logLevel = "info"
	}

	if dbPath == "" {
		dbPath = filepath.Join(outputPath, ".wandersort.db")
	}

	if dbLocationPath == "" {
		dbLocationPath = filepath.Join(appDir, "location.db")
	}

	if workers != "" {
		if n, err := strconv.Atoi(workers); err == nil && n > 0 {
			workerCount = n
		}
	}

	if port == "" {
		port = "8080"
	}

	if updateIntervalStr == "" {
		updateIntervalStr = "5s"
	}

	updateInterval, err := time.ParseDuration(updateIntervalStr)
	if err != nil {
		return nil, err
	}

	if finalizeTimeoutStr == "" {
		finalizeTimeoutStr = "15s"
	}

	finalizeTimeout, err := time.ParseDuration(finalizeTimeoutStr)
	if err != nil {
		return nil, err
	}

	return &Configuration{
		ServerPort:      port,
		Host:            host,
		DatabasePath:    dbPath,
		DbLocationPath:  dbLocationPath,
		OutputPath:      outputPath,
		LogLevel:        logLevel,
		LogConsole:      true,
		LogFile:         logPath,
		Workers:         workerCount,
		UpdateInterval:  updateInterval,
		FinalizeTimeout: finalizeTimeout,
		AppDir:          appDir,
		BinDir:          binDir,
	}, nil
}
