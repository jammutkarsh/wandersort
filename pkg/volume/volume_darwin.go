// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin

package volume

import (
	"fmt"
	"os/exec"
	"regexp"
	"syscall"

	"golang.org/x/sys/unix"
)

// diskutil's plist output for a volume carries its UUID as a key/string pair.
var volumeUUIDPattern = regexp.MustCompile(`<key>VolumeUUID</key>\s*<string>([0-9A-Fa-f-]+)</string>`)

// classForPath decides the storage class from the same two sources uuidForPath
// already uses: statfs for the mount, and diskutil's plist for the device
func classForPath(path string) (Class, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return ClassUnknown, fmt.Errorf("statfs %q: %w", path, err)
	}

	// A network mount wins outright: its backing device is unknowable from
	// here, and diskutil has nothing to say about it either
	if networkFilesystems[cString(st.Fstypename[:])] || st.Flags&unix.MNT_LOCAL == 0 {
		return ClassNetwork, nil
	}

	mount := cString(st.Mntonname[:])
	out, err := exec.Command("diskutil", "info", "-plist", mount).Output()
	if err != nil {
		return ClassUnknown, fmt.Errorf("diskutil info %q: %w", mount, err)
	}
	return classFromDiskutil(out), nil
}

// classFromDiskutil reads the class out of a `diskutil info -plist` document.
// Pure, so the decision ladder is testable without a real volume
func classFromDiskutil(plist []byte) Class {
	// A RAID or Fusion volume can be mixed; the slow read is the safe read
	if raid, ok := plistBool(plist, "RAIDMaster"); ok && raid {
		return ClassRotational
	}

	solid, ok := plistBool(plist, "SolidState")
	if !ok {
		return ClassUnknown
	}
	if !solid {
		return ClassRotational
	}
	// SolidState alone cannot separate an internal NVMe from a thumb drive —
	// that is what the bus is for
	if bus, ok := plistString(plist, "BusProtocol"); ok && bus == "USB" {
		return ClassRemovable
	}
	return ClassSolidState
}

// plistBool reads a <key>k</key><true/> or <false/> pair
func plistBool(plist []byte, key string) (value, found bool) {
	m := regexp.
		MustCompile(`<key>` + regexp.QuoteMeta(key) + `</key>\s*<(true|false)/>`).
		FindSubmatch(plist)
	if m == nil {
		return false, false
	}
	return string(m[1]) == "true", true
}

// plistString reads a <key>k</key><string>v</string> pair
func plistString(plist []byte, key string) (value string, found bool) {
	m := regexp.
		MustCompile(`<key>` + regexp.QuoteMeta(key) + `</key>\s*<string>([^<]*)</string>`).
		FindSubmatch(plist)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

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

// FreeBytes returns the bytes available to the current user on the volume
// containing path
func FreeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("statfs %q: %w", path, err)
	}
	return st.Bavail * uint64(st.Bsize), nil
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
