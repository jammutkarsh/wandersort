package path

import (
	"path/filepath"

	"golang.org/x/text/unicode/norm"
)

// ToLibrary normalizes a path the app created (target_path, folder names) for
// storage: "/" separators and NFC, so the database is the same on every OS.
func ToLibrary(p string) string {
	return norm.NFC.String(filepath.ToSlash(p))
}

// FromLibrary converts a stored in-library path back to the OS's native
// separator for use with os/filepath calls.
func FromLibrary(p string) string {
	return filepath.FromSlash(p)
}

// ToSourcePath normalizes an OS-given path for storage: only the separator
// changes (filepath.ToSlash), never a name's bytes. NFC-folding would make
// NFD names unfindable on Linux and could merge distinct files.
func ToSourcePath(p string) string {
	return filepath.ToSlash(p)
}

// FromSourcePath reverses ToSourcePath for use with os/filepath calls.
func FromSourcePath(p string) string {
	return filepath.FromSlash(p)
}
