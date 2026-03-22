package util

import (
	"os"
	"path/filepath"
	"strings"
)

// IsDirectory checks if a path string points to a directory.
func (u *Util) IsDirectory(path string) (bool, error) {
	if p, err := u.RealPath(path); err != nil {
		return false, err
	} else {
		path = p
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		// Return false and the error if the path does not exist or other issues occur
		return false, err
	}
	return fileInfo.IsDir(), nil
}

func (u *Util) RealPath(p string) (string, error) {
	p = u.ExpandPath(p)
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	return absPath, nil
}

// ExpandPath expands a leading "~/" to the user's home directory.
// Non-home-relative paths are returned unchanged.
func (u *Util) ExpandPath(path string) string {
	return u.resolveHomePath(path)
}

// ContractPath converts an absolute path under the user's home directory to a
// human-readable "~" form.
func (u *Util) ContractPath(path string) string {
	cleanPath := filepath.Clean(path)
	home := filepath.Clean(u.HomeDir)

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
func (u *Util) MakeRelative(filePath, sourceRoot string) (string, error) {
	absFile, err := u.RealPath(filePath)
	if err != nil {
		return "", err
	}
	absRoot, err := u.RealPath(sourceRoot)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(absRoot, absFile)
	if err != nil {
		return "", err
	}

	return rel, nil
}

// MakeAbsolute returns an absolute path for filePath using sourceRoot if
// filePath is not already absolute.
func (u *Util) MakeAbsolute(filePath, sourceRoot string) string {
	if filepath.IsAbs(filePath) {
		return filepath.Clean(filePath)
	}

	if strings.HasPrefix(filePath, "~/") {
		return filepath.Clean(u.ExpandPath(filePath))
	}

	expandedRoot := u.ExpandPath(sourceRoot)
	if expandedRoot == "~" {
		expandedRoot = u.HomeDir
	}

	return filepath.Clean(filepath.Join(expandedRoot, filePath))
}

// resolveHomePath converts ~/path to absolute path.
// Example: "~/Photos/2023" -> "/home/username/Photos/2023"
func (u *Util) resolveHomePath(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(u.HomeDir, path[2:])
	}
	return path
}
