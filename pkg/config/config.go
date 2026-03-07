package config

import (
	"os"
	"path/filepath"
	"strconv"
)

type Configuration struct {
	ServerPort   string
	DatabasePath string
	OutputPath   string
	OTelEnabled  bool
	LogLevel     string
	LogConsole   bool
	LogFile      string
	Workers      int
}

func Load() (*Configuration, error) {
	outputPath, logPath := os.Getenv("OUTPUT_PATH"), os.Getenv("LOG_FILE")
	if outputPath == "" {
		home, _ := os.UserHomeDir()
		outputPath = filepath.Join(home, "WanderSort_Library")
	}

	if logPath == "" {
		logPath = filepath.Join(outputPath, ".wandersort.log")
	}

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join(outputPath, ".wandersort.db")
	}

	const defaultWorkers = 5
	workers := defaultWorkers
	if v := os.Getenv("WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			workers = n
		}
	}

	return &Configuration{
		ServerPort:   os.Getenv("PORT"),
		OutputPath:   outputPath,
		OTelEnabled:  os.Getenv("OTEL_ENABLED") == "true",
		LogLevel:     logLevel,
		LogFile:      logPath,
		LogConsole:   true,
		DatabasePath: dbPath,
		Workers:      workers,
	}, nil
}
