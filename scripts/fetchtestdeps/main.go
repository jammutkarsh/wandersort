// fetchtestdeps pre-downloads exiftool and the location database into a
// gitignored directory (test/deps) through install.Coordinator, with visible
// progress, so tests never download silently mid-run. Run via `make test-deps`.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func main() {
	dir := "test/deps"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "fetchtestdeps:", err)
		os.Exit(1)
	}
	ctx := context.Background()
	log := logger.New(nil)
	dbPath := filepath.Join(dir, install.LocationDBFileName)

	// location first, on its own Coordinator: it is the one tests depend on
	loc := install.New(install.Options{
		LocationDBPath: dbPath,
		Log:            log,
		OnProgress:     printProgress,
	})
	loc.StartLocationOnly(ctx, nil)
	if _, err := loc.Location(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "fetchtestdeps: location db:", err)
		os.Exit(1)
	}

	// no test needs the real exiftool binary, so its failure only warns
	exif := install.New(install.Options{
		ExecutablePath: filepath.Join(dir, "bin"),
		LocationDBPath: dbPath,
		Log:            log,
		OnProgress:     printProgress,
	})
	exif.Start(ctx)
	if _, err := exif.Exiftool(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "fetchtestdeps: exiftool (non-fatal, no test needs it yet):", err)
	}

	fmt.Println("test deps ready:", dir)
}

// printProgress renders one self-overwriting progress line per phase.
func printProgress(phase string, done, total int64) {
	if total <= 0 {
		return
	}
	pct := float64(done) / float64(total) * 100
	fmt.Printf("\r%-10s %6.1f%%  (%d/%d bytes)", phase, pct, done, total)
	if done >= total {
		fmt.Println()
	}
}
