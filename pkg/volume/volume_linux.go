// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package volume

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// FreeBytes returns the bytes available to the current user on the volume
// containing path
func FreeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("statfs %q: %w", path, err)
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// uuidForPath resolves the block device backing path's longest mount-point
// prefix from /proc/self/mounts, then matches that device against the
// /dev/disk/by-uuid symlink table
func uuidForPath(path string) (string, error) {
	device, err := deviceForPath(path)
	if err != nil {
		return "", err
	}

	const byUUID = "/dev/disk/by-uuid"
	entries, err := os.ReadDir(byUUID)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", byUUID, err)
	}
	for _, e := range entries {
		target, err := filepath.EvalSymlinks(filepath.Join(byUUID, e.Name()))
		if err == nil && target == device {
			return e.Name(), nil
		}
	}
	return "", fmt.Errorf("no by-uuid entry for device %q", device)
}

// deviceForPath returns the source device of the longest mount point that is
// a prefix of path
func deviceForPath(path string) (string, error) {
	data, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return "", fmt.Errorf("read mounts: %w", err)
	}

	device, bestLen := "", -1
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		mount := unescapeMount(fields[1])
		if !coversPath(mount, path) || len(mount) <= bestLen {
			continue
		}
		device, bestLen = fields[0], len(mount)
	}
	if device == "" {
		return "", fmt.Errorf("no mount covers %q", path)
	}

	resolved, err := filepath.EvalSymlinks(device)
	if err != nil {
		return device, nil // virtual sources (tmpfs, overlay) aren't symlinks
	}
	return resolved, nil
}

// coversPath reports whether mount is "/", equal to path, or an ancestor of it
func coversPath(mount, path string) bool {
	return mount == "/" || mount == path || strings.HasPrefix(path, mount+"/")
}

// unescapeMount decodes the octal escapes /proc/self/mounts uses for
// whitespace in mount points (e.g. "\040" for a space)
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if code, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(code))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
