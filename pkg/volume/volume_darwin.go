//go:build darwin

package volume

import (
	"fmt"
	"os/exec"
	"regexp"
	"syscall"
)

// diskutil's plist output for a volume carries its UUID as a key/string pair.
// ponytail: regexp over the plist instead of a plist library — the pair shape
// is stable; adopt howett.net/plist if more fields are ever needed
var volumeUUIDPattern = regexp.MustCompile(`<key>VolumeUUID</key>\s*<string>([0-9A-Fa-f-]+)</string>`)

// uuidForPath finds the mount point via statfs, then asks diskutil for the
// volume UUID of that mount
func uuidForPath(path string) (string, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return "", fmt.Errorf("statfs %q: %w", path, err)
	}
	mount := cString(st.Mntonname[:])

	out, err := exec.Command("diskutil", "info", "-plist", mount).Output()
	if err != nil {
		return "", fmt.Errorf("diskutil info %q: %w", mount, err)
	}
	m := volumeUUIDPattern.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("no VolumeUUID in diskutil output for %q", mount)
	}
	return string(m[1]), nil
}

// cString converts a NUL-terminated C char array to a Go string
func cString(chars []int8) string {
	b := make([]byte, 0, len(chars))
	for _, c := range chars {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}
