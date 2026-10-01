package volume

import (
	"path/filepath"
	"strings"
)

// ThisComputer is the name Label gives a path on the machine's own disk.
const ThisComputer = "This computer"

// mountRoots are the folders removable and network drives mount under, and
// how many segments below them name the drive (/media/<user>/<drive>).
var mountRoots = []struct {
	prefix string
	skip   int
}{
	{"/Volumes/", 0},
	{"/run/media/", 1},
	{"/media/", 1},
	{"/mnt/", 0},
}

// Label names the drive a path lives on, for a person: the mount's folder name
// for a removable or network drive, the drive letter on Windows, and
// ThisComputer otherwise. It reads only the path, so it works for a drive
// that is no longer plugged in.
// ponytail: a drive mounted anywhere else reads as ThisComputer; ask the OS
// for the mount point if that misleads.
func Label(path string) string {
	if v := filepath.VolumeName(path); v != "" {
		return v
	}
	slashed := filepath.ToSlash(path)
	for _, root := range mountRoots {
		rest, ok := strings.CutPrefix(slashed, root.prefix)
		if !ok {
			continue
		}
		parts := strings.Split(rest, "/")
		if len(parts) > root.skip && parts[root.skip] != "" {
			return parts[root.skip]
		}
	}
	return ThisComputer
}
