package install

import (
	"archive/tar"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/atomicfile"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

// This file is the exiftool half of pkg/install's job: pkg/exiftool only
// runs the already-installed binary and parses its output.

const (
	exiftoolVersion = "13.59"

	// windows is runtime.GOOS's value for Windows; the only one needing a branch
	windows = "windows"

	filesBaseURL        = "https://wandersort.app/files"
	releaseMetaFileName = "exiftool.json"

	// releaseMetaTimeout bounds the whole manifest fetch, connect to last byte; it is under 1 KB
	releaseMetaTimeout = 5 * time.Second
)

// releaseMeta is published by .github/workflows/publish-r2.yml alongside
// the mirrored archives, e.g.:
//
//	{"version":"13.59","updated":"...","files":{"darwin":{"name":"exiftool-13.59-darwin.tar.zst","sha256":"...","size":123}}}
type releaseMeta struct {
	Files   map[string]releaseMetaFile `json:"files"`
	Version string                     `json:"version"`
}

type releaseMetaFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// fetchReleaseMeta downloads the mirrored archives' checksum manifest, the only
// trusted source of expected names and hashes.
func fetchReleaseMeta(ctx context.Context) (releaseMeta, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, releaseMetaTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, filesBaseURL+"/"+releaseMetaFileName, nil)
	if err != nil {
		return releaseMeta{}, fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return releaseMeta{}, fmt.Errorf("GET %s: %w", releaseMetaFileName, terminalDownloadErr(ctx, err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return releaseMeta{}, fmt.Errorf("GET %s: unexpected status %s", releaseMetaFileName, resp.Status)
	}

	var meta releaseMeta
	// published elsewhere, so matched the forgiving way
	if err := json.UnmarshalRead(resp.Body, &meta, json.MatchCaseInsensitiveNames(true)); err != nil {
		return releaseMeta{}, fmt.Errorf("decode %s: %w", releaseMetaFileName, terminalDownloadErr(ctx, err))
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

// setupExiftool uses exiftool from $PATH or binDir when new enough, else
// downloads it into binDir. onProgress may be nil.
func setupExiftool(ctx context.Context, log logger.Logger, binDir string, onProgress func(done, total int64)) (string, error) {
	if path, err := findExiftool(log, binDir); err == nil {
		return path, nil
	}
	return installExiftool(ctx, log, binDir, onProgress)
}

func findExiftool(log logger.Logger, binDir string) (string, error) {
	// bundled first: a PATH copy is often too old, and probing it costs a start
	binaryPath := filepath.Join(binDir, exiftoolBin())
	if _, err := os.Stat(binaryPath); err == nil {
		if ok, _ := checkVersion(binaryPath, log); ok {
			log.Info("exiftool found", "path", binaryPath)
			return binaryPath, nil
		}
		log.Info("exiftool is outdated; installing bundled version", "path", binaryPath)
		return "", fmt.Errorf("exiftool at %s is outdated", binaryPath)
	}

	if path, err := exec.LookPath(exiftoolBin()); err == nil {
		if ok, _ := checkVersion(path, log); ok {
			log.Info("exiftool found on PATH", "path", path)
			return path, nil
		}
		log.Info("exiftool on PATH is outdated; installing bundled version")
	}

	return "", fmt.Errorf("exiftool not found at %s", binaryPath)
}

// bundledExiftool is the exiftool shipped in binDir, checked but never
// replaced: an outdated one is an error, not a download.
func bundledExiftool(log logger.Logger, binDir string) (string, error) {
	binaryPath := filepath.Join(binDir, exiftoolBin())
	if _, err := os.Stat(binaryPath); err != nil {
		return "", fmt.Errorf("bundled exiftool: %w", err)
	}
	ok, err := checkVersion(binaryPath, log)
	if err != nil {
		return "", fmt.Errorf("bundled exiftool: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("bundled exiftool at %s is outdated", binaryPath)
	}
	return binaryPath, nil
}

func installExiftool(ctx context.Context, log logger.Logger, binDir string, onProgress func(done, total int64)) (string, error) {
	if err := downloadAndExtractExiftool(ctx, binDir, log, onProgress); err != nil {
		return "", fmt.Errorf("install exiftool: %w", err)
	}

	binaryPath := filepath.Join(binDir, exiftoolBin())
	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath, nil
	}

	return "", fmt.Errorf("exiftool not found after install at %s", binaryPath)
}

func downloadAndExtractExiftool(ctx context.Context, binDir string, log logger.Logger, onProgress func(done, total int64)) error {
	staging := binDir + stagingSuffix
	if err := atomicfile.MkdirAll(staging); err != nil {
		return fmt.Errorf("create dir %q: %w", staging, err)
	}

	meta, err := fetchReleaseMeta(ctx)
	if err != nil {
		return fmt.Errorf("fetch release metadata: %w", err)
	}
	fileMeta, ok := meta.Files[runtime.GOOS]
	if !ok {
		return fmt.Errorf("automatic install not supported on %s", runtime.GOOS)
	}

	archiveName := filepath.Join(staging, fileMeta.Name)
	url := filesBaseURL + "/" + fileMeta.Name

	log.Info("Downloading exiftool (first run only)…", logger.UserKey, true, "dir", path.New().RelativeToHome(binDir), "url", url, "os", runtime.GOOS)

	// Reuse a cached archive only if its checksum still matches — a mismatch
	// means either corruption or tampering, so re-download either way
	if _, err := os.Stat(archiveName); err == nil {
		if sum, err := fileSHA256(archiveName); err == nil && sum == fileMeta.SHA256 {
			log.Info("exiftool checksum verified", "path", archiveName, "hash", sum)
			log.Info("using cached archive", "path", archiveName)
		} else {
			log.Warn("cached archive checksum mismatch; re-downloading", "path", archiveName)
			os.Remove(archiveName)
		}
	}

	if _, err := os.Stat(archiveName); err != nil {
		// the progress row already shows this; no log line
		if err := downloadFile(ctx, archiveName, url, fileMeta.SHA256, onProgress); err != nil {
			return fmt.Errorf("download: %w", err)
		}
		log.Info("exiftool downloaded")
		log.Info("exiftool checksum verified", "path", archiveName, "hash", fileMeta.SHA256)
	}

	if err := installArchive(archiveName, binDir); err != nil {
		return err
	}
	log.Info("extracted exiftool", "dir", binDir)

	if err := os.RemoveAll(staging); err != nil {
		log.Warn("failed to remove downloaded archive", "path", staging, "error", err)
	}
	return nil
}

const (
	// stagingSuffix names the folder beside binDir an install downloads and
	// extracts into, so binDir only ever holds a whole install.
	stagingSuffix = ".staging"
	// extractedDirName is the fully extracted install inside the staging folder.
	extractedDirName = "extracted"
	// retiredSuffix names the previous install while the new one takes its place.
	retiredSuffix = ".old"
)

// installArchive extracts archive beside binDir and only then swaps it in: a
// crash mid-extract leaves the previous install (or none) whole, never a
// launcher beside half its library.
func installArchive(archive, binDir string) error {
	extracted := filepath.Join(filepath.Dir(archive), extractedDirName)
	if err := os.RemoveAll(extracted); err != nil {
		return fmt.Errorf("clear %s: %w", extracted, err)
	}
	if err := extractTarZst(archive, extracted); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	if err := onlyInstalled(binDir, extracted); err != nil {
		return err
	}
	// a crash between the two renames leaves no binDir: the next run installs again
	retired := binDir + retiredSuffix
	if err := os.RemoveAll(retired); err != nil {
		return fmt.Errorf("clear %s: %w", retired, err)
	}
	if err := os.Rename(binDir, retired); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("set aside %s: %w", binDir, err)
	}
	if err := os.Rename(extracted, binDir); err != nil {
		return fmt.Errorf("move %s into place: %w", extracted, err)
	}
	if err := atomicfile.SyncDir(filepath.Dir(binDir)); err != nil {
		return fmt.Errorf("sync %s: %w", filepath.Dir(binDir), err)
	}
	return os.RemoveAll(retired)
}

// ErrFolderNotOurs refuses to replace an install folder holding anything an
// exiftool install doesn't: the swap would delete it.
var ErrFolderNotOurs = errors.New("the exiftool folder holds other files; not replacing it")

// onlyInstalled reports ErrFolderNotOurs if binDir holds a name the new
// install doesn't: binDir is replaced whole, so only an earlier install may be in it.
func onlyInstalled(binDir, extracted string) error {
	old, err := os.ReadDir(binDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", binDir, err)
	}
	fresh, err := os.ReadDir(extracted)
	if err != nil {
		return fmt.Errorf("read %s: %w", extracted, err)
	}
	ours := make(map[string]bool, len(fresh))
	for _, e := range fresh {
		ours[e.Name()] = true
	}
	for _, e := range old {
		if !ours[e.Name()] {
			return fmt.Errorf("%w: %s", ErrFolderNotOurs, filepath.Join(binDir, e.Name()))
		}
	}
	return nil
}

// extractTarZst extracts a CI-normalized tar.zst (flat layout, launcher
// renamed) into destDir, every file and folder synced to disk.
func extractTarZst(tzstPath, destDir string) error {
	zr, closeZr, err := openZstd(tzstPath)
	if err != nil {
		return err
	}
	defer closeZr()

	if err := atomicfile.MkdirAll(destDir); err != nil {
		return fmt.Errorf("mkdir %s: %w", destDir, err)
	}
	dirs := map[string]bool{destDir: true}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			for dir := range dirs {
				if err := atomicfile.SyncDir(dir); err != nil {
					return fmt.Errorf("sync %s: %w", dir, err)
				}
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		dst := filepath.Join(destDir, filepath.FromSlash(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := atomicfile.MkdirAll(dst); err != nil {
				return fmt.Errorf("mkdir %s: %w", dst, err)
			}
			dirs[dst] = true
		case tar.TypeReg:
			if err := atomicfile.MkdirAll(filepath.Dir(dst)); err != nil {
				return fmt.Errorf("mkdir parent %s: %w", dst, err)
			}
			dirs[filepath.Dir(dst)] = true
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create %s: %w", dst, err)
			}
			_, copyErr := io.Copy(out, tr)
			syncErr := out.Sync()
			closeErr := out.Close()
			if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
				return fmt.Errorf("write %s: %w", dst, err)
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
	ok, err := versionAtLeast(ver)
	if err == nil && !ok {
		log.Info("exiftool version below requirement", "have", ver, "want", exiftoolVersion)
	}
	return ok, err
}

// versionAtLeast reports whether ver ("major.minor", as exiftool -ver prints it) is at least exiftoolVersion.
func versionAtLeast(ver string) (bool, error) {
	parts := strings.SplitN(ver, ".", 2)
	if len(parts) < 2 {
		return false, fmt.Errorf("unexpected version format: %s", ver)
	}

	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if err := errors.Join(majorErr, minorErr); err != nil {
		return false, fmt.Errorf("parse version %s: %w", ver, err)
	}

	reqParts := strings.SplitN(exiftoolVersion, ".", 2)
	reqMajor, _ := strconv.Atoi(reqParts[0])
	reqMinor, _ := strconv.Atoi(reqParts[1])
	return major > reqMajor || (major == reqMajor && minor >= reqMinor), nil
}
