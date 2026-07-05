package path

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Resolver struct {
	HomeDir string
}

func New() *Resolver {
	home, _ := os.UserHomeDir()
	return &Resolver{HomeDir: home}
}

// IsDirectory checks if a path string points to a directory.
func (r *Resolver) IsDirectory(path string) (bool, error) {
	if p, err := r.RealPath(path); err != nil {
		return false, err
	} else {
		path = p
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("stat %q: %w", path, err)
	}
	return fileInfo.IsDir(), nil
}

// RealPath resolves symlinks and returns the canonical absolute path of p.
func (r *Resolver) RealPath(p string) (string, error) {
	p = r.ExpandPath(p)
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("eval symlinks %q: %w", p, err)
	}
	absPath, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("abs %q: %w", resolved, err)
	}
	return absPath, nil
}

// ExpandPath expands a leading "~/" to the user's home directory.
// Non-home-relative paths are returned unchanged.
func (r *Resolver) ExpandPath(path string) string {
	return r.resolveHomePath(path)
}

// RelativeToHome converts an absolute path to
// a path relative wrt user's home directory if it is under the home directory.
func (r *Resolver) RelativeToHome(path string) string {
	cleanPath := filepath.Clean(path)
	home := filepath.Clean(r.HomeDir)

	if cleanPath == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(cleanPath, prefix) {
		suffix := strings.TrimPrefix(cleanPath, home)
		return "~" + suffix
	}

	return path
}

// MakeRelative returns filePath relative to sourceRoot.
func (r *Resolver) MakeRelative(filePath, sourceRoot string) (string, error) {
	absFile, err := r.RealPath(filePath)
	if err != nil {
		return "", err
	}
	absRoot, err := r.RealPath(sourceRoot)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(absRoot, absFile)
	if err != nil {
		return "", fmt.Errorf("rel %q to %q: %w", absFile, absRoot, err)
	}

	return rel, nil
}

// MakeAbsolute returns an absolute path for filePath using sourceRoot if
// filePath is not already absolute.
func (r *Resolver) MakeAbsolute(filePath, sourceRoot string) string {
	if filepath.IsAbs(filePath) {
		return filepath.Clean(filePath)
	}

	if strings.HasPrefix(filePath, "~/") {
		return filepath.Clean(r.ExpandPath(filePath))
	}

	expandedRoot := r.ExpandPath(sourceRoot)
	if expandedRoot == "~" {
		expandedRoot = r.HomeDir
	}

	return filepath.Clean(filepath.Join(expandedRoot, filePath))
}

// resolveHomePath converts ~/path to absolute path.
// Example: "~/Photos/2023" -> "/home/username/Photos/2023"
func (r *Resolver) resolveHomePath(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(r.HomeDir, strings.TrimPrefix(path, "~/"))
	}
	return path
}
