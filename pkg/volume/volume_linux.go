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
	device, _, err := deviceForPath(path)
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

// deviceForPath returns the source device and filesystem type of the longest
// mount point that is a prefix of path
func deviceForPath(path string) (device, fstype string, err error) {
	data, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return "", "", fmt.Errorf("read mounts: %w", err)
	}

	bestLen := -1
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		mount := unescapeMount(fields[1])
		if !coversPath(mount, path) || len(mount) <= bestLen {
			continue
		}
		device, fstype, bestLen = fields[0], fields[2], len(mount)
	}
	if device == "" {
		return "", "", fmt.Errorf("no mount covers %q", path)
	}

	resolved, err := filepath.EvalSymlinks(device)
	if err != nil {
		return device, fstype, nil // virtual sources (tmpfs, overlay) aren't symlinks
	}
	return resolved, fstype, nil
}

// classForPath asks sysfs whether the backing block device seeks. The fstype
// is checked first: a network mount's backing device is unknowable from here
func classForPath(path string) (Class, error) {
	device, fstype, err := deviceForPath(path)
	if err != nil {
		return ClassUnknown, err
	}
	if networkFilesystems[fstype] {
		return ClassNetwork, nil
	}

	name := strings.TrimPrefix(device, "/dev/")
	if name == "" || strings.Contains(name, "/") {
		return ClassUnknown, nil // tmpfs, overlay, anything not a block device
	}

	// A whole disk has its own queue/; a partition does not, so a failed read
	// is the signal to retry on the disk the partition belongs to. Asking
	// sysfs beats pattern-matching the name — /dev/loop0 and /dev/sda1 look
	// alike to a stripping rule and are not alike at all
	rotational, err := sysfsFlag(name, "queue/rotational")
	if err != nil {
		base := blockBase(name)
		if base == "" {
			return ClassUnknown, nil
		}
		if rotational, err = sysfsFlag(base, "queue/rotational"); err != nil {
			return ClassUnknown, err
		}
		name = base
	}
	if rotational {
		return ClassRotational, nil
	}
	// rotational=0 cannot separate an NVMe from a USB 2.0 thumb drive; the
	// removable flag is the closest linux gets to darwin's BusProtocol
	if removable, err := sysfsFlag(name, "removable"); err == nil && removable {
		return ClassRemovable, nil
	}
	return ClassSolidState, nil
}

// sysfsFlag reads a /sys/block/<base>/<attr> file holding "0" or "1"
func sysfsFlag(base, attr string) (bool, error) {
	name := filepath.Join("/sys/block", base, attr)
	data, err := os.ReadFile(name)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", name, err)
	}
	return strings.TrimSpace(string(data)) == "1", nil
}

// blockBase strips a partition suffix to the whole-disk name sysfs is keyed
// by: sda1 -> sda, nvme0n1p2 -> nvme0n1, mmcblk0p1 -> mmcblk0. Only called
// once the name has already failed to be a whole disk, so a name that ends in
// no partition at all ("") means "stop guessing"
func blockBase(name string) string {
	// device-mapper (LVM, LUKS) numbers whole devices, not partitions, so
	// stripping the digits would name something that does not exist
	if strings.HasPrefix(name, "dm-") {
		return ""
	}
	// nvme0n1p2 and mmcblk0p1 mark the partition with a "p" after a digit;
	// sda1 just appends the number
	if i := strings.LastIndex(name, "p"); i > 0 && allDigits(name[i+1:]) && isDigit(name[i-1]) {
		return name[:i]
	}
	if base := strings.TrimRight(name, "0123456789"); base != name {
		return base
	}
	return ""
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
