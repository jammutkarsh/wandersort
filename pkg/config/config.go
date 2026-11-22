package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// DefaultLibrary is the output folder's name under $HOME until the user picks
// one; the settings wizard shows it as the placeholder.
const DefaultLibrary = "WandersortLibrary"

const defaultDBFileName = ".wandersort.db"

// LocationDBFileName is the location database's name in the app folder.
const LocationDBFileName = "location.db"

// Configuration is one run's settings: the library's own Settings (loaded once
// the library is open) plus paths this process computes.
type Configuration struct {
	Settings

	// appDir holds the app's own files: location database, logs, library history
	appDir string

	AppDBPath      string
	LocationDBPath string
	// LogDir holds persisted run logs, apart from any library
	LogDir         string
	ExecutablePath string

	// Workers sizes the CPU-bound goroutine and exiftool pools: NumCPU, not
	// a setting. Disk reads are throttled by storage class instead.
	Workers int
}

// New builds the runtime configuration with default settings and, as the
// output folder, the most recently used library, even when its drive is
// unplugged: opening it then says so rather than starting another library.
func New() (*Configuration, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home directory: %w", err)
	}
	return NewAt(filepath.Join(home, ".wandersort"))
}

// NewAt is New keeping the app's own files (location database, tools, logs,
// library history) in appDir instead of ~/.wandersort.
func NewAt(appDir string) (*Configuration, error) {
	// absolute now: a program started from a launcher often runs in "/",
	// and the logs, history and tools must stay where the caller meant
	appDir, err := filepath.Abs(appDir)
	if err != nil {
		return nil, fmt.Errorf("resolve app folder %s: %w", appDir, err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home directory: %w", err)
	}
	cfg := &Configuration{
		Settings:       DefaultSettings(),
		appDir:         appDir,
		LocationDBPath: filepath.Join(appDir, LocationDBFileName),
		LogDir:         filepath.Join(appDir, "logs"),
		Workers:        runtime.NumCPU(),
		ExecutablePath: filepath.Join(appDir, "bin"),
	}

	out := filepath.Join(home, DefaultLibrary)
	if recent := cfg.readHistory(historyFileName); len(recent) > 0 {
		out = recent[0]
	}
	cfg.SetOutput(out)
	return cfg, nil
}

// SetOutput points the configuration at an output folder. Only before the
// library is open: a library's folder never moves. A relative dir is taken
// from the working directory: SQLite reads a relative file: URI as a host.
func (cfg *Configuration) SetOutput(dir string) {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	cfg.AppDBPath = DBPath(dir)
}

// DBPath is the database of the library in the output folder dir.
func DBPath(dir string) string { return filepath.Join(dir, defaultDBFileName) }

// OutputDir is the library folder this run works in.
func (cfg *Configuration) OutputDir() string { return filepath.Dir(cfg.AppDBPath) }

// CheckLibrary's refusals of a folder that can't be a library.
var (
	ErrInsideLibrary = errors.New("inside the library")
	ErrBackupOnly    = errors.New("has a backup but no database; restore the database from its backup")
	ErrNotEmpty      = errors.New("isn't empty or a library; pick another folder")
)

// osClutter is what an OS drops into any folder it has shown; a folder holding
// only these is still empty as far as the user is concerned.
var osClutter = map[string]bool{".DS_Store": true, "Thumbs.db": true, "desktop.ini": true}

// ErrNetworkLibrary refuses a library on a network drive: file and SQLite locks
// there don't keep two computers out, so the database could be corrupted.
var ErrNetworkLibrary = errors.New("network drives aren't supported; pick a local or USB drive")

// InsideLibrary reports the library whose folder is above dir, if any. A
// library never holds another one, and nothing in it is a source to scan.
func InsideLibrary(dir string) (root string, inside bool) {
	for p := filepath.Dir(filepath.Clean(dir)); ; p = filepath.Dir(p) {
		if _, err := os.Stat(filepath.Join(p, defaultDBFileName)); err == nil {
			return p, true
		}
		if parent := filepath.Dir(p); parent == p {
			return "", false
		}
	}
}

// CheckLibrary reports whether dir may be an output folder: local, and missing,
// empty, or already a library. Anything else would receive files on top of content the
// database doesn't know. OS clutter and a lone lock file don't count.
func CheckLibrary(dir string) error {
	shown := path.New().Friendly // messages name folders the way the screens do, never by absolute path
	if volume.IsNetwork(dir) {
		return fmt.Errorf("output folder %s: %w", shown(dir), ErrNetworkLibrary)
	}
	if root, inside := InsideLibrary(dir); inside {
		return fmt.Errorf("%s: %w %s; pick a folder outside it", shown(dir), ErrInsideLibrary, shown(root))
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("output folder %s: %w", shown(dir), err)
	}
	for _, e := range entries {
		if e.Name() == defaultDBFileName {
			return nil
		}
	}
	for _, e := range entries {
		if e.Name() == db.BackupFileName {
			return fmt.Errorf("%s: %w", shown(dir), ErrBackupOnly)
		}
	}
	for _, e := range entries {
		if n := e.Name(); !osClutter[n] && n != lock.OutputFileName {
			return fmt.Errorf("%s: %w", shown(dir), ErrNotEmpty)
		}
	}
	return nil
}
