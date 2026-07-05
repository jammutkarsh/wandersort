package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

const (
	defaultAppDir      = ".wandersort"
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
	OutputPath     string
	LogLevel       string
	LogConsole     bool
	LogFile        string
	Workers        int
	AppDir         string
	BinDir         string
}

// Load reads environment variables and builds the application Configuration.
// Defaults: info log level, port 8080, WanderSortLibrary output dir, and
// runtime.NumCPU() workers. Returns error only for an invalid WORKERS env var.
func Load() (*Configuration, error) {
	var (
		outputPath  = os.Getenv("OUTPUT_PATH")
		logLevel    = os.Getenv("LOG_LEVEL")
		port        = os.Getenv("PORT")
		workerCount = runtime.NumCPU()
	)
	home, _ := os.UserHomeDir()
	wandersortDir := filepath.Join(home, defaultAppDir)
	dbLocationPath := filepath.Join(wandersortDir, locationDBFileName)

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
		outputPath = filepath.Join(home, defaultLibraryDir)
	}
	logPath := filepath.Join(outputPath, defaultLogFileName)
	dbPath := filepath.Join(outputPath, defaultDBFileName)

	if logLevel == "" {
		logLevel = defaultLogLevel
	}

	if port == "" {
		port = defaultPort
	}

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
		OutputPath:     outputPath,
		LogLevel:       logLevel,
		LogConsole:     true,
		LogFile:        logPath,
		Workers:        workerCount,
		AppDir:         appDir,
		BinDir:         binDir,
	}, nil
}
