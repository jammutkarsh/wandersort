package exiftool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

const (
	exiftoolVersion = "13.59"

	// Name of the exiftool executable, same on all platforms
	exiftoolName = "exiftool"

	// GOOS constants — runtime.GOOS uses these string values
	windows = "windows"
	macOS   = "darwin"
	linux   = "linux"
)

// downloadURLs maps GOOS to the SourceForge download URL for the portable archive
var downloadURLs = map[string]string{
	windows: fmt.Sprintf("https://sourceforge.net/projects/exiftool/files/exiftool-%s_64.zip/download", exiftoolVersion),
	linux:   fmt.Sprintf("https://sourceforge.net/projects/exiftool/files/Image-ExifTool-%s.tar.gz/download", exiftoolVersion),
	macOS:   fmt.Sprintf("https://sourceforge.net/projects/exiftool/files/ExifTool-%s.pkg/download", exiftoolVersion),
}

// Verify checks exiftool is available, either on $PATH or in WanderSort's
// own install directory. If missing, it downloads a copy into ~/.wandersort/bin
func Verify(log logger.Logger, binDir string) (string, error) {
	if path, err := exec.LookPath(exiftoolName); err == nil {
		log.Info("exiftool found on PATH", "path", path)
		return path, nil
	}

	binaryPath := filepath.Join(binDir, exiftoolName)

	if _, err := os.Stat(binaryPath); err == nil {
		log.Info("exiftool found in wandersort bin", "path", binaryPath)
		return binaryPath, nil
	}

	log.Info("exiftool not found; downloading", "dir", binDir, "os", runtime.GOOS)
	if err := install(binDir, log); err != nil {
		return "", fmt.Errorf("install exiftool: %w", err)
	}

	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath, nil
	}

	return "", fmt.Errorf("exiftool not found after install at %s", binaryPath)
}

// install downloads and stores the exiftool archive in binDir
func install(binDir string, log logger.Logger) error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create dir %q: %w", binDir, err)
	}

	url, ok := downloadURLs[runtime.GOOS]
	if !ok {
		return fmt.Errorf("automatic install not supported on %s", runtime.GOOS)
	}

	// SourceForge /download URLs redirect; the real filename is the segment before it
	parts := strings.Split(strings.TrimRight(url, "/"), "/")
	archiveName := filepath.Join(binDir, parts[len(parts)-1])
	// TODO: move download file to a better package
	if err := db.DownloadFile(archiveName, url); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	switch runtime.GOOS {
	case windows:
		if err := installFromZip(archiveName, binDir, log); err != nil {
			return fmt.Errorf("zip: %w", err)
		}
	case macOS:
		if err := installFromPkg(archiveName, binDir, log); err != nil {
			return fmt.Errorf("pkg: %w", err)
		}
	default:
		if err := installFromTarGz(archiveName, binDir, log); err != nil {
			return fmt.Errorf("tar.gz: %w", err)
		}
	}

	if err := os.Remove(archiveName); err != nil {
		log.Warn("failed to remove downloaded archive", "path", archiveName, "error", err)
	}
	return nil
}

// installFromZip extracts the Windows zip and renames the launcher
//
// Zip structure:
//
//	exiftool-{ver}_64/
//	  exiftool(-k).exe       ← PE32+ launcher
//	  exiftool_files/        ← Strawberry Perl runtime (perl.exe, perl532.dll, lib/…)
func installFromZip(zipPath, binDir string, log logger.Logger) error {
	tmpDir, err := os.MkdirTemp("", "exiftool-zip-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use system unzip to extract into a temp directory
	if err := exec.Command("unzip", "-q", zipPath, "-d", tmpDir).Run(); err != nil {
		return fmt.Errorf("unzip: %w", err)
	}

	// Find the single versioned top-level directory (e.g. exiftool-13.59_64/)
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return fmt.Errorf("read tmp dir: %w", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return fmt.Errorf("unexpected zip structure")
	}

	// Move everything from that directory into binDir
	srcDir := filepath.Join(tmpDir, entries[0].Name())
	if err := moveContents(srcDir, binDir); err != nil {
		return fmt.Errorf("move contents: %w", err)
	}

	// Rename the launcher for cross-platform consistency: exiftool(-k).exe → exiftool
	launcher := filepath.Join(binDir, "exiftool(-k).exe")
	if _, err := os.Stat(launcher); err == nil {
		if err := os.Rename(launcher, filepath.Join(binDir, exiftoolName)); err != nil {
			return fmt.Errorf("rename launcher: %w", err)
		}
	}

	log.Info("extracted windows zip", "dir", binDir)
	return nil
}

// installFromTarGz extracts the Linux / portable tar.gz
//
// Tarball structure:
//
//	Image-ExifTool-{ver}/
//	  exiftool              ← Perl script
//	  lib/                  ← Perl library modules (~20 MB)
//	  html/, fmt_files/, …
func installFromTarGz(tgzPath, destDir string, log logger.Logger) error {
	tmpDir, err := os.MkdirTemp("", "exiftool-tar-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use system tar to extract into a temp directory
	cmd := exec.Command("tar", "xzf", tgzPath, "-C", tmpDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tar: %w\n%s", err, output)
	}

	// Find the single versioned top-level directory (e.g. Image-ExifTool-13.59/)
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return fmt.Errorf("read tmp dir: %w", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return fmt.Errorf("unexpected tarball structure")
	}

	// Move everything from that directory into destDir
	srcDir := filepath.Join(tmpDir, entries[0].Name())
	if err := moveContents(srcDir, destDir); err != nil {
		return fmt.Errorf("move contents: %w", err)
	}

	log.Info("extracted tar.gz", "dir", destDir)
	return nil
}

// installFromPkg extracts the macOS .pkg and copies files to binDir
//
// Pkg layout (via pkgutil --expand-full):
//
//	Payload/usr/local/bin/
//	  exiftool              ← Perl script
//	  lib/                  ← Perl library modules
func installFromPkg(pkgPath, destDir string, log logger.Logger) error {
	extractDir, err := os.MkdirTemp("", "exiftool-pkg-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(extractDir)

	// Extract the xar archive using macOS's pkgutil
	cmd := exec.Command("pkgutil", "--expand-full", pkgPath, extractDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pkgutil --expand-full: %w\n%s", err, output)
	}

	// Move everything from Payload/usr/local/bin into destDir
	srcDir := filepath.Join(extractDir, "Payload", "usr", "local", "bin")
	if err := moveContents(srcDir, destDir); err != nil {
		return fmt.Errorf("move pkg contents: %w", err)
	}

	log.Info("extracted macOS pkg", "dir", destDir)
	return nil
}

// moveContents moves all entries from srcDir into dstDir using os.Rename
func moveContents(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", srcDir, err)
	}
	for _, entry := range entries {
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(dstDir, entry.Name())
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("rename %s -> %s: %w", src, dst, err)
		}
	}
	return nil
}
