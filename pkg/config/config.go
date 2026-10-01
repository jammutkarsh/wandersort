package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// DefaultLibrary is the output folder's name under $HOME until the user picks
// one; the settings wizard shows it as the placeholder.
const DefaultLibrary = "WandersortLibrary"

const (
	defaultDBFileName  = ".wandersort.db"
	locationDBFileName = "location.db"
)

// Configuration is one run's settings: the library's own Settings (loaded once
// the library is open) plus paths this process computes.
type Configuration struct {
	Settings

	// appDir is ~/.wandersort: location database, logs, library history
	appDir string

	// Workers sizes the CPU-bound goroutine and exiftool pools: NumCPU, not
	// a setting. Disk reads are throttled by storage class instead.
	Workers        int
	AppDBPath      string
	LocationDBPath string
	// LogDir holds persisted run logs, apart from any library
	LogDir         string
	ExecutablePath string
}

// New builds the runtime configuration with default settings and, as the
// output folder, the most recently used library that still exists.
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

// SetOutput points the configuration at an output folder. Only before the
// library is open: a library's folder never moves.
func (cfg *Configuration) SetOutput(dir string) {
	cfg.AppDBPath = filepath.Join(dir, defaultDBFileName)
}

// OutputDir is the library folder this run works in.
func (cfg *Configuration) OutputDir() string { return filepath.Dir(cfg.AppDBPath) }

// osClutter is what an OS drops into any folder it has shown; a folder holding
// only these is still empty as far as the user is concerned.
var osClutter = map[string]bool{".DS_Store": true, "Thumbs.db": true, "desktop.ini": true}

// ErrNetworkLibrary refuses a library on a network drive: file and SQLite locks
// there don't keep two computers out, so the database could be corrupted.
var ErrNetworkLibrary = errors.New("it is on a network drive (NAS, SMB, NFS), which WanderSort doesn't support; pick a folder on a local or USB drive")

// CheckLibrary reports whether dir may be an output folder: local, and missing,
// empty, or already a library. Anything else would receive files on top of content the
// database doesn't know. OS clutter and a lone lock file don't count.
func CheckLibrary(dir string) error {
	if volume.IsNetwork(dir) {
		return fmt.Errorf("output folder %s: %w", dir, ErrNetworkLibrary)
	}
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
