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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/utils"
)

const (
	exiftoolVersion = "13.59"

	// windows is runtime.GOOS's value for Windows; the only one needing a branch
	windows = "windows"

	filesBaseURL        = "https://wandersort.utkarshchourasia.in/files"
	releaseMetaFileName = "exiftool.json"
)

// releaseMeta is published by .github/workflows/publish-r2.yml alongside
// the mirrored archives, e.g.:
//
//	{"version":"13.59","updated":"...","files":{"darwin":{"name":"exiftool-13.59-darwin.tar.gz","sha256":"...","size":123}}}
type releaseMeta struct {
	Version string                     `json:"version"`
	Files   map[string]releaseMetaFile `json:"files"`
}

type releaseMetaFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// fetchReleaseMeta downloads and parses the checksum manifest for the
// mirrored archives — the only trusted source for expected file names and
// hashes, so a compromised R2 bucket can't just swap an archive out silently
func fetchReleaseMeta(ctx context.Context) (releaseMeta, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, filesBaseURL+"/"+releaseMetaFileName, nil)
	if err != nil {
		return releaseMeta{}, fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return releaseMeta{}, fmt.Errorf("GET %s: %w", releaseMetaFileName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return releaseMeta{}, fmt.Errorf("GET %s: unexpected status %s", releaseMetaFileName, resp.Status)
	}

	var meta releaseMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return releaseMeta{}, fmt.Errorf("decode %s: %w", releaseMetaFileName, err)
	}
	return meta, nil
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
// onProgress (may be nil) is called with (bytesDownloaded, totalBytes) while
// fetching the bundled archive, so a TUI can show a real progress bar without
// the per-byte counts polluting the file log (which only sees the download's
// start/done milestones).
func Setup(ctx context.Context, log logger.Logger, binDir string, onProgress func(done, total int64)) (string, error) {
	if path, err := findExiftool(log, binDir); err == nil {
		return path, nil
	}
	return installExiftool(ctx, log, binDir, onProgress)
}

// Installed reports whether a usable exiftool is already present (PATH or
// binDir, correct version), so the caller can skip a download-progress screen
// when Setup would be a no-op.
func Installed(log logger.Logger, binDir string) bool {
	_, err := findExiftool(log, binDir)
	return err == nil
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

func installExiftool(ctx context.Context, log logger.Logger, binDir string, onProgress func(done, total int64)) (string, error) {
	if err := install(ctx, binDir, log, onProgress); err != nil {
		return "", fmt.Errorf("install exiftool: %w", err)
	}

	binaryPath := filepath.Join(binDir, exiftoolBin())
	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath, nil
	}

	return "", fmt.Errorf("exiftool not found after install at %s", binaryPath)
}

func install(ctx context.Context, binDir string, log logger.Logger, onProgress func(done, total int64)) error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create dir %q: %w", binDir, err)
	}

	meta, err := fetchReleaseMeta(ctx)
	if err != nil {
		return fmt.Errorf("fetch release metadata: %w", err)
	}
	fileMeta, ok := meta.Files[runtime.GOOS]
	if !ok {
		return fmt.Errorf("automatic install not supported on %s", runtime.GOOS)
	}

	archiveName := filepath.Join(binDir, fileMeta.Name)
	url := filesBaseURL + "/" + fileMeta.Name

	log.Info("Downloading ExifTool…", logger.UserKey, true, "dir", path.New().RelativeToHome(binDir), "url", url, "os", runtime.GOOS)

	// Reuse a cached archive only if its checksum still matches — a mismatch
	// means either corruption or tampering, so re-download either way
	if _, err := os.Stat(archiveName); err == nil {
		if sum, err := utils.SHA256File(archiveName); err == nil && sum == fileMeta.SHA256 {
			log.Info("exiftool checksum verified", logger.UserKey, true, "path", archiveName, "hash", sum)
			log.Info("using cached archive", "path", archiveName)
		} else {
			log.Warn("cached archive checksum mismatch; re-downloading", "path", archiveName)
			os.Remove(archiveName)
		}
	}

	if _, err := os.Stat(archiveName); err != nil {
		log.Info("Downloading exiftool", logger.UserKey, true, logger.PhaseKey, "exiftool", logger.EventKey, "start")
		if err := utils.DownloadFileProgress(ctx, archiveName, url, onProgress); err != nil {
			return fmt.Errorf("download: %w", err)
		}
		log.Info("exiftool downloaded", logger.UserKey, true, logger.PhaseKey, "exiftool", logger.EventKey, "done")
	}

	sum, err := utils.SHA256File(archiveName)
	if err != nil {
		return fmt.Errorf("checksum %s: %w", archiveName, err)
	}
	if sum != fileMeta.SHA256 {
		os.Remove(archiveName)
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", fileMeta.Name, sum, fileMeta.SHA256)
	}
	log.Info("exiftool checksum verified", logger.UserKey, true, "path", archiveName, "hash", sum)

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

	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if err := errors.Join(majorErr, minorErr); err != nil {
		return false, fmt.Errorf("parse version %s: %w", ver, err)
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
