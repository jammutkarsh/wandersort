package exiftool

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

const (
	// installDir is where WanderSort keeps its own copy of exiftool,
	// independent of any system-wide install.
	installDir = ".wandersort/bin"

	// windowsDownloadURL points to the latest portable Windows build.
	// exiftool.org does not expose a stable "latest" alias, so this is
	// updated manually when bumping the bundled version.
	windowsDownloadURL = "https://sourceforge.net/projects/exiftool/files/exiftool-13.59_64.zip/download"
	// macDownloadURL points to the latest macOS installer package.
	macDownloadURL = "https://exiftool.org/ExifTool-13.59.pkg"
)

// Check verifies exiftool is available, either on $PATH or in WanderSort's
// own install directory. If missing, it downloads a copy into ~/.wandersort/bin.
func Check(log logger.Logger) (string, error) {
	if path, err := exec.LookPath("exiftool"); err == nil {
		log.Info("exiftool found on PATH", "path", path)
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	binDir := filepath.Join(home, installDir)
	binPath := filepath.Join(binDir, binaryName())

	if _, err := os.Stat(binPath); err == nil {
		log.Info("exiftool found in wandersort bin", "path", binPath)
		return binPath, nil
	}

	log.Info("exiftool not found; downloading", "dir", binDir, "os", runtime.GOOS)
	if err := install(binDir, log); err != nil {
		return "", fmt.Errorf("install exiftool: %w", err)
	}

	if _, err := os.Stat(binPath); err != nil {
		return "", fmt.Errorf("exiftool still missing after install: %w", err)
	}

	log.Info("exiftool installed", "path", binPath)
	return binPath, nil
}

// binaryName returns the platform-specific exiftool executable name.
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "exiftool.exe"
	}
	return "exiftool"
}

// install downloads and unpacks exiftool into binDir for the current OS.
func install(binDir string, log logger.Logger) error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create dir %q: %w", binDir, err)
	}

	switch runtime.GOOS {
	case "windows":
		return installWindows(binDir, log)
	case "darwin":
		return installDarwin(binDir, log)
	default:
		return fmt.Errorf("automatic install not supported on %s; install exiftool manually and ensure it is on PATH", runtime.GOOS)
	}
}

// installWindows downloads the portable zip and extracts exiftool.exe.
func installWindows(binDir string, log logger.Logger) error {
	zipPath := filepath.Join(binDir, "exiftool.zip")
	if err := downloadFile(zipPath, windowsDownloadURL); err != nil {
		return fmt.Errorf("download windows build: %w", err)
	}
	defer os.Remove(zipPath)

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if !strings.EqualFold(filepath.Base(f.Name), "exiftool(-k).exe") {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip entry: %w", err)
		}
		defer rc.Close()

		dest := filepath.Join(binDir, "exiftool.exe")
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return fmt.Errorf("create %s: %w", dest, err)
		}
		defer out.Close()

		if _, err := io.Copy(out, rc); err != nil {
			return fmt.Errorf("extract exiftool.exe: %w", err)
		}

		log.Info("extracted exiftool.exe", "path", dest)
		return nil
	}

	return fmt.Errorf("exiftool(-k).exe not found in downloaded zip")
}

// installDarwin downloads the macOS package and runs the installer.
func installDarwin(binDir string, log logger.Logger) error {
	pkgPath := filepath.Join(binDir, "exiftool.pkg")
	if err := downloadFile(pkgPath, macDownloadURL); err != nil {
		return fmt.Errorf("download macos build: %w", err)
	}
	defer os.Remove(pkgPath)

	cmd := exec.Command("installer", "-pkg", pkgPath, "-target", "/")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run installer: %w (output: %s)", err, output)
	}

	log.Info("ran macos exiftool installer", "output", string(output))
	return nil
}

// downloadFile fetches url and writes the body to dest atomically.
func downloadFile(dest, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".dl-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("rename to %s: %w", dest, err)
	}

	return nil
}
