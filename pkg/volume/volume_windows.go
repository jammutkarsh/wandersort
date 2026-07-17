//go:build windows

package volume

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// uuidForPath resolves the Windows volume GUID for path: GetVolumePathName
// finds the mount point (drive letter or mounted folder — both can change
// between sessions), and GetVolumeNameForVolumeMountPoint returns the stable
// volume GUID path `\\?\Volume{...}\`. The GUID inside the braces is returned
// so the value matches the UUID shape of the other platforms.
// ponytail: verified by cross-compilation only — no Windows hardware in CI yet
func uuidForPath(path string) (string, error) {
	pathW, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", fmt.Errorf("encode path %q: %w", path, err)
	}
	mount := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(pathW, &mount[0], uint32(len(mount))); err != nil {
		return "", fmt.Errorf("volume path for %q: %w", path, err)
	}

	// The mount point must keep its trailing backslash for the GUID lookup
	mountStr := windows.UTF16ToString(mount)
	if !strings.HasSuffix(mountStr, `\`) {
		mountStr += `\`
	}
	mountW, err := windows.UTF16PtrFromString(mountStr)
	if err != nil {
		return "", fmt.Errorf("encode mount %q: %w", mountStr, err)
	}

	// 50 UTF-16 chars fit `\\?\Volume{GUID}\` per the winapi contract
	guidPath := make([]uint16, 50)
	if err := windows.GetVolumeNameForVolumeMountPoint(mountW, &guidPath[0], uint32(len(guidPath))); err != nil {
		return "", fmt.Errorf("volume name for %q: %w", mountStr, err)
	}

	s := windows.UTF16ToString(guidPath)
	lo, hi := strings.Index(s, "{"), strings.Index(s, "}")
	if lo < 0 || hi <= lo {
		return "", fmt.Errorf("unexpected volume GUID path %q", s)
	}
	return s[lo+1 : hi], nil
}

// FreeBytes returns the bytes available to the current user on the volume
// containing path
func FreeBytes(path string) (uint64, error) {
	pathW, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("encode path %q: %w", path, err)
	}
	var freeToCaller, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(pathW, &freeToCaller, &total, &totalFree); err != nil {
		return 0, fmt.Errorf("free space for %q: %w", path, err)
	}
	return freeToCaller, nil
}
