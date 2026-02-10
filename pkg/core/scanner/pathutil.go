package scanner

import (
	"os"
	"path/filepath"
	"strings"
)

// PathUtil handles path normalization and expansion
type PathUtil struct {
	homeDir string
}

func NewPathUtil() (*PathUtil, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &PathUtil{homeDir: homeDir}, nil
}

// ExpandPath converts ~/path to absolute path.
// Example: "~/Photos/2023" -> "/home/username/Photos/2023"
func (pu *PathUtil) ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(pu.homeDir, path[2:])
	}
	return path
}

// ContractPath converts absolute path to ~/path if under home directory.
// Example: "/home/username/Photos/2023" -> "~/Photos/2023"
func (pu *PathUtil) ContractPath(path string) string {
	if after, ok := strings.CutPrefix(path, pu.homeDir); ok {
		relativePath := after
		relativePath = strings.TrimPrefix(relativePath, string(filepath.Separator))
		return filepath.Join("~", relativePath)
	}
	return path
}

// MakeRelative converts absolute file path to relative path from source root.
// Example: filePath="/home/user/Photos/2023/img.jpg", root="/home/user/Photos" -> "2023/img.jpg"
func (pu *PathUtil) MakeRelative(filePath, sourceRoot string) (string, error) {
	absFile := pu.ExpandPath(filePath)
	absRoot := pu.ExpandPath(sourceRoot)

	relPath, err := filepath.Rel(absRoot, absFile)
	if err != nil {
		return "", err
	}

	return relPath, nil
}

// MakeAbsolute converts relative path back to absolute using source root.
// Example: relPath="2023/img.jpg", root="~/Photos" -> "/home/user/Photos/2023/img.jpg"
func (pu *PathUtil) MakeAbsolute(relPath, sourceRoot string) string {
	absRoot := pu.ExpandPath(sourceRoot)
	return filepath.Join(absRoot, relPath)
}
