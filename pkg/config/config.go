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
	// GroupBy overrides vfs.DefaultConfig's GroupBy when non-empty; "none"
	// means no levels at all (flat Year/Month). Validated against
	// vfs.GroupBy* in cli.
	GroupBy []string
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
		ServerPort:     defaultPort,
		AppDBPath:      filepath.Join(outputPath, DefaultDBFileName),
		LocationDBPath: filepath.Join(appDir, locationDBFileName),
		LogLevel:       defaultLogLevel,
		LogConsole:     true,
		LogFile:        filepath.Join(outputPath, DefaultLogFileName),
		Workers:        runtime.NumCPU(),
		ExecutablePath: executablesDirectory,
	}, nil
}
