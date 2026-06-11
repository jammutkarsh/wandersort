package config

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Configuration struct {
	ServerPort      string
	Host            string
	DatabasePath    string
	OutputPath      string
	LogLevel        string
	LogConsole      bool
	LogFile         string
	Workers         int
	UpdateInterval  time.Duration
	FinalizeTimeout time.Duration
	LocationDBPath  string
}

func Load() (*Configuration, error) {
	const defaultWorkers = 5
	var (
		outputPath         = os.Getenv("OUTPUT_PATH")
		logPath            = os.Getenv("LOG_FILE")
		logLevel           = os.Getenv("LOG_LEVEL")
		dbPath             = os.Getenv("DB_PATH")
		workers            = os.Getenv("WORKERS")
		port               = os.Getenv("PORT")
		host               = os.Getenv("HOST")
		updateIntervalStr  = os.Getenv("UPDATE_INTERVAL")
		finalizeTimeoutStr = os.Getenv("FINALIZE_TIMEOUT")
		locationDBPath     = os.Getenv("LOCATION_DB_PATH")
		workerCount        = defaultWorkers
	)

	if outputPath == "" {
		home, _ := os.UserHomeDir()
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

	if locationDBPath == "" {
		locationDBPath = filepath.Join(outputPath, ".wandersort.locationdb")
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
		OutputPath:      outputPath,
		LogLevel:        logLevel,
		LogFile:         logPath,
		LogConsole:      true,
		DatabasePath:    dbPath,
		Workers:         workerCount,
		UpdateInterval:  updateInterval,
		FinalizeTimeout: finalizeTimeout,
		LocationDBPath:  locationDBPath,
	}, nil
}
