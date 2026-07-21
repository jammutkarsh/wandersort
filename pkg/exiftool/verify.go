// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package exiftool

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/utils"
)

const (
	exiftoolVersion = "13.59"

	// GOOS constants — runtime.GOOS uses these string values
	windows = "windows"
	macOS   = "darwin"
	linux   = "linux"
)

// supportedGOOS are the platforms .github/workflows/publish-r2.yml mirrors
var supportedGOOS = map[string]bool{windows: true, macOS: true, linux: true}

// downloadURL returns WanderSort's R2 mirror of the portable archive for
// goos. Every platform is repackaged by CI into the same flat tar.gz layout
// (contents = binDir, no wrapping directory, launcher pre-renamed), so a
// single naming scheme and a single extractor cover all of them.
func downloadURL(goos string) (string, bool) {
	if !supportedGOOS[goos] {
		return "", false
	}
	return fmt.Sprintf("https://wandersort.utkarshchourasia.in/files/exiftool-%s-%s.tar.gz", exiftoolVersion, goos), true
}

// exiftoolBin returns the platform-specific binary name
// On Windows the .exe extension is required for execution
func exiftoolBin() string {
	if runtime.GOOS == windows {
		return "exiftool.exe"
	}
	return "exiftool"
}

// Setup checks exiftool is available, either on $PATH or in WanderSort's
// own install directory. If the found version is below the requirement, it
// downloads and installs a bundled copy into ~/.wandersort/bin
func Setup(ctx context.Context, log logger.Logger, binDir string) (string, error) {
	if path, err := findExiftool(log, binDir); err == nil {
		return path, nil
	}
	return installExiftool(ctx, log, binDir)
}

func findExiftool(log logger.Logger, binDir string) (string, error) {
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

	return "", fmt.Errorf("exiftool not found at %s", binaryPath)
}

func installExiftool(ctx context.Context, log logger.Logger, binDir string) (string, error) {
	log.Info("Downloading ExifTool…", logger.UserKey, true, "dir", binDir, "os", runtime.GOOS)
	if err := install(ctx, binDir, log); err != nil {
		return "", fmt.Errorf("install exiftool: %w", err)
	}

	binaryPath := filepath.Join(binDir, exiftoolBin())
	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath, nil
	}

	return "", fmt.Errorf("exiftool not found after install at %s", binaryPath)
}

func install(ctx context.Context, binDir string, log logger.Logger) error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create dir %q: %w", binDir, err)
	}

	url, ok := downloadURL(runtime.GOOS)
	if !ok {
		return fmt.Errorf("automatic install not supported on %s", runtime.GOOS)
	}

	// https://wandersort.utkarshchourasia.in/files/exiftool-13.59-darwin.tar.gz -> exiftool-13.59-darwin.tar.gz
	parts := strings.Split(strings.TrimRight(url, "/"), "/")
	archiveName := filepath.Join(binDir, parts[len(parts)-1])

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
		if err := utils.DownloadFile(ctx, archiveName, url); err != nil {
			return fmt.Errorf("download: %w", err)
		}
	}

	if err := extractTarGz(archiveName, binDir); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	log.Info("extracted exiftool", "dir", binDir)

	if err := os.Remove(archiveName); err != nil {
		log.Warn("failed to remove downloaded archive", "path", archiveName, "error", err)
	}
	return nil
}

// extractTarGz extracts a tar.gz produced by publish-r2.yml directly into
// destDir. CI already normalizes every platform's upstream archive to this
// layout (contents flat at the root, launcher pre-renamed), so extraction
// needs no per-platform unwrapping or renaming step.
func extractTarGz(tgzPath, destDir string) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", tgzPath, err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		dst := filepath.Join(destDir, filepath.FromSlash(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", dst, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("mkdir parent %s: %w", dst, err)
			}
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create %s: %w", dst, err)
			}
			_, copyErr := io.Copy(out, tr)
			out.Close()
			if copyErr != nil {
				return fmt.Errorf("write %s: %w", dst, copyErr)
			}
		}
	}
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

// archiveValid verifies the downloaded tar.gz is not corrupt by reading it
// through to EOF, which surfaces gzip checksum and tar structure errors
func archiveValid(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return false
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		_, err := tr.Next()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
	}
}
