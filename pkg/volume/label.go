package volume

import (
	"path/filepath"
	"strings"
)

// ThisComputer is the name Label gives a path on the machine's own disk.
const ThisComputer = "This computer"

// mountRoots are where removable and network drives mount, and how many segments below name the drive.
var mountRoots = []struct {
	prefix string
	skip   int
}{
	{"/Volumes/", 0},
	{"/run/media/", 1},
	{"/media/", 1},
	{"/mnt/", 0},
}

// Label names a path's drive from the path alone: mount folder, Windows drive letter, or ThisComputer.
func Label(path string) string {
	// ponytail: a drive mounted anywhere else reads as ThisComputer; ask the OS for the mount point if that misleads
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
