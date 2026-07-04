package exiftool

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
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

	// Name of the exiftool executable, same on all platforms.
	exiftoolName = "exiftool"

	// GOOS constants — runtime.GOOS uses these string values.
	windows = "windows"
	macOS   = "darwin"
	linux   = "linux"
)

// downloadURLs maps GOOS to the SourceForge download URL for the portable archive.
var downloadURLs = map[string]string{
	windows: fmt.Sprintf("https://sourceforge.net/projects/exiftool/files/exiftool-%s_64.zip/download", exiftoolVersion),
	linux:   fmt.Sprintf("https://sourceforge.net/projects/exiftool/files/Image-ExifTool-%s.tar.gz/download", exiftoolVersion),
	macOS:   fmt.Sprintf("https://sourceforge.net/projects/exiftool/files/ExifTool-%s.pkg/download", exiftoolVersion),
}

// Verify checks exiftool is available, either on $PATH or in WanderSort's
// own install directory. If missing, it downloads a copy into ~/.wandersort/bin.
func Verify(log logger.Logger, binDir string) (string, error) {
	if path, err := exec.LookPath(exiftoolName); err == nil {
		log.Info("exiftool found on PATH", "path", path)
		return path, nil
	}

	binPath := filepath.Join(binDir, exiftoolName)

	if _, err := os.Stat(binPath); err == nil {
		log.Info("exiftool found in wandersort bin", "path", binPath)
		return binPath, nil
	}

	log.Info("exiftool not found; downloading", "dir", binDir, "os", runtime.GOOS)
	if err := install(binDir, log); err != nil {
		return "", fmt.Errorf("install exiftool: %w", err)
	}

	if _, err := os.Stat(binPath); err == nil {
		return binPath, nil
	}

	return "", fmt.Errorf("exiftool not found after install at %s", binPath)
}

// install downloads and stores the exiftool archive in binDir.
// The archive must be kept — exiftool needs its support files at runtime.
func install(binDir string, log logger.Logger) error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create dir %q: %w", binDir, err)
	}

	url, ok := downloadURLs[runtime.GOOS]
	if !ok {
		return fmt.Errorf("automatic install not supported on %s", runtime.GOOS)
	}

	// SourceForge /download URLs redirect; the real filename is the segment before it.
	parts := strings.Split(strings.TrimRight(url, "/"), "/")
	archivePath := filepath.Join(binDir, parts[len(parts)-1])
	if err := db.DownloadFile(archivePath, url); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	switch runtime.GOOS {
	case windows:
		if err := installFromZip(archivePath, binDir, log); err != nil {
			return fmt.Errorf("zip: %w", err)
		}
	case macOS:
		if err := installFromPkg(archivePath, binDir, log); err != nil {
			return fmt.Errorf("pkg: %w", err)
		}
	default:
		if err := installFromTarGz(archivePath, binDir, log); err != nil {
			return fmt.Errorf("tar.gz: %w", err)
		}
	}
	return nil
}

// installFromZip extracts the Windows zip and renames the launcher.
//
// Zip structure:
//
//	exiftool-{ver}_64/
//	  exiftool(-k).exe       ← PE32+ launcher
//	  exiftool_files/        ← Strawberry Perl runtime (perl.exe, perl532.dll, lib/…)
func installFromZip(zipPath, binDir string, log logger.Logger) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	// Determine the top-level directory inside the zip (exiftool-{ver}_64/).
	var topDir string
	for _, f := range r.File {
		if strings.Contains(f.Name, "/") {
			topDir = f.Name[:strings.Index(f.Name, "/")+1]
			break
		}
	}
	if topDir == "" {
		return fmt.Errorf("unexpected zip structure — no directory found")
	}

	for _, f := range r.File {
		// Strip the top-level directory prefix.
		rel := strings.TrimPrefix(f.Name, topDir)
		if rel == "" {
			continue
		}

		dest := filepath.Join(binDir, rel)

		// Handle the launcher rename: exiftool(-k).exe → exiftool
		if strings.EqualFold(filepath.Base(rel), "exiftool(-k).exe") {
			dest = filepath.Join(binDir, exiftoolName)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", rel, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open %s: %w", rel, err)
		}

		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			rc.Close()
			return fmt.Errorf("create %s: %w", rel, err)
		}

		if _, err := io.Copy(out, rc); err != nil {
			rc.Close()
			out.Close()
			return fmt.Errorf("write %s: %w", rel, err)
		}
		rc.Close()
		out.Close()
	}

	log.Info("extracted windows zip", "dir", binDir)
	return nil
}

// installFromTarGz extracts the Linux / portable tar.gz.
//
// Tarball structure:
//
//	Image-ExifTool-{ver}/
//	  exiftool              ← Perl script
//	  lib/                  ← Perl library modules (~20 MB)
//	  html/, fmt_files/, …
func installFromTarGz(tgzPath, destDir string, log logger.Logger) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
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
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		// Strip the top-level Image-ExifTool-{ver}/ prefix.
		parts := strings.SplitN(hdr.Name, "/", 2)
		if len(parts) < 2 {
			continue
		}
		rel := parts[1]
		if rel == "" {
			continue
		}

		dest := filepath.Join(destDir, rel)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", rel, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
			}
			out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, hdr.FileInfo().Mode())
			if err != nil {
				return fmt.Errorf("create %s: %w", rel, err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return fmt.Errorf("write %s: %w", rel, err)
			}
			out.Close()
		}
	}

	log.Info("extracted tar.gz", "dir", destDir)
	return nil
}

// installFromPkg extracts the macOS .pkg and copies files to binDir.
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

	cmd := exec.Command("pkgutil", "--expand-full", pkgPath, extractDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pkgutil --expand-full: %w\n%s", err, output)
	}

	srcDir := filepath.Join(extractDir, "Payload", "usr", "local", "bin")
	if err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(srcDir, path)
		if rel == "." {
			return nil
		}

		dest := filepath.Join(destDir, rel)

		if info.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}

		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()

		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}

		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer out.Close()

		_, err = io.Copy(out, in)
		return err
	}); err != nil {
		return fmt.Errorf("copy pkg files: %w", err)
	}

	log.Info("extracted macOS pkg", "dir", destDir)
	return nil
}
