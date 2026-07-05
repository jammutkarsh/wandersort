package exiftool

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

const (
	exiftoolVersion = "13.59"

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

// exiftoolBin returns the platform-specific binary name
// On Windows the .exe extension is required for execution
func exiftoolBin() string {
	if runtime.GOOS == windows {
		return "exiftool.exe"
	}
	return "exiftool"
}

// Verify checks exiftool is available, either on $PATH or in WanderSort's
// own install directory. If the found version is below the requirement, it
// downloads and installs a bundled copy into ~/.wandersort/bin
func Verify(log logger.Logger, binDir string) (string, error) {
	// Check PATH — only accept if version meets requirement
	if path, err := exec.LookPath(exiftoolBin()); err == nil {
		if ok, _ := checkVersion(path, log); ok {
			log.Info("exiftool found on PATH", "path", path)
			return path, nil
		}
		log.Info("exiftool on PATH is outdated; installing bundled version")
	}

	binaryPath := filepath.Join(binDir, exiftoolBin())

	// Check binDir install — only accept if version meets requirement
	if _, err := os.Stat(binaryPath); err == nil {
		if ok, _ := checkVersion(binaryPath, log); ok {
			log.Info("exiftool found", "path", binaryPath)
			return binaryPath, nil
		}
		log.Info("exiftool is outdated; installing bundled version", "path", binaryPath)
	}

	log.Info("exiftool not found; downloading", "dir", binDir, "os", runtime.GOOS)
	if err := install(context.Background(), binDir, log); err != nil {
		return "", fmt.Errorf("install exiftool: %w", err)
	}

	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath, nil
	}

	return "", fmt.Errorf("exiftool not found after install at %s", binaryPath)
}

func install(ctx context.Context, binDir string, log logger.Logger) error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create dir %q: %w", binDir, err)
	}

	url, ok := downloadURLs[runtime.GOOS]
	if !ok {
		return fmt.Errorf("automatic install not supported on %s", runtime.GOOS)
	}

	// https://sourceforge.net/projects/exiftool/files/exiftool-13.59_64.zip/download -> exiftool-13.59_64.zip
	parts := strings.Split(strings.TrimRight(url, "/"), "/")
	archiveName := filepath.Join(binDir, parts[len(parts)-2])

	// Reuse a cached archive if it passes integrity check
	if _, err := os.Stat(archiveName); err == nil {
		if archiveValid(archiveName) {
			log.Info("using cached archive", "path", archiveName)
		} else {
			log.Warn("cached archive corrupt; re-downloading", "path", archiveName)
			os.Remove(archiveName)
		}
	}

	if _, err := os.Stat(archiveName); err != nil {
		if err := db.DownloadFile(ctx, archiveName, url); err != nil {
			return fmt.Errorf("download: %w", err)
		}
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

	if err := extractZip(zipPath, tmpDir); err != nil {
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

	// Rename the launcher: exiftool(-k).exe → exiftool.exe (or exiftool on non-Windows)
	launcher := filepath.Join(binDir, "exiftool(-k).exe")
	if _, err := os.Stat(launcher); err == nil {
		if err := os.Rename(launcher, filepath.Join(binDir, exiftoolBin())); err != nil {
			return fmt.Errorf("rename launcher: %w", err)
		}
	}

	log.Info("extracted windows zip", "dir", binDir)
	return nil
}

// extractZip extracts all files from a zip archive into destDir using archive/zip
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	// Windows doesn't have zip/unzup command preinstalled
	// hence using Go's standard library to extract the archive
	for _, f := range r.File {
		dst := filepath.Join(destDir, filepath.FromSlash(f.Name))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, f.Mode()); err != nil {
				return fmt.Errorf("mkdir %s: %w", dst, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("mkdir parent %s: %w", dst, err)
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			return fmt.Errorf("create %s: %w", dst, err)
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return fmt.Errorf("write %s: %w", dst, copyErr)
		}
	}
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

	// Use system tar — available on all major Linux distros
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
	parentDir, err := os.MkdirTemp("", "exiftool-pkg-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(parentDir)

	// Let pkgutil create the output directory itself
	extractDir := filepath.Join(parentDir, "expanded")
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

// checkVersion runs exiftool -ver on the given binary and returns true if its
// version is at least exiftoolVersion
func checkVersion(path string, log logger.Logger) (bool, error) {
	output, err := exec.Command(path, "-ver").Output()
	if err != nil {
		return false, fmt.Errorf("run %s -ver: %w", path, err)
	}

	ver := strings.TrimSpace(string(output))
	parts := strings.SplitN(ver, ".", 2)
	if len(parts) < 2 {
		return false, fmt.Errorf("unexpected version format: %s", ver)
	}

	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false, fmt.Errorf("parse version %s: %v %v", ver, err1, err2)
	}

	// Parse required version
	reqParts := strings.SplitN(exiftoolVersion, ".", 2)
	reqMajor, _ := strconv.Atoi(reqParts[0])
	reqMinor, _ := strconv.Atoi(reqParts[1])

	if major > reqMajor || (major == reqMajor && minor >= reqMinor) {
		return true, nil
	}

	log.Info("exiftool version below requirement", "have", ver, "want", exiftoolVersion)
	return false, nil
}

// archiveValid verifies the downloaded archive is not corrupt
func archiveValid(path string) bool {
	switch {
	case strings.HasSuffix(path, ".zip"):
		// zip.OpenReader verifies the central directory and CRCs of all entries
		r, err := zip.OpenReader(path)
		if err != nil {
			return false
		}
		r.Close()
		return true
	case strings.HasSuffix(path, ".tar.gz"):
		return exec.Command("tar", "tzf", path).Run() == nil
	case strings.HasSuffix(path, ".pkg"):
		return exec.Command("pkgutil", "--payload-files", path).Run() == nil
	}
	return false
}
