// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package volume

// Class is how a volume behaves under concurrent reads. It is the storage
// half of a decision the CPU has no business making: eight readers is the
// worst case for a spinning disk and near-optimal for an NVMe one.
//
// Detection is the initial guess, not the answer — a RAID can be mixed, a NAS
// says nothing useful about its backing store, and rotational=0 cannot tell an
// NVMe from a USB 2.0 stick on every platform. Consumers should treat the
// class as a starting point, and ClassUnknown as a conservative answer rather
// than a failure.
type Class int

const (
	// ClassUnknown is a first-class answer, never an error: an unsupported
	// platform, an unmountable path, or a device that reports nothing.
	ClassUnknown Class = iota
	// ClassRotational is seek-penalised: an HDD, and anything mixed enough
	// that the slow read is the safe read (RAID, Fusion).
	ClassRotational
	// ClassSolidState is an internal SSD/NVMe — no seek penalty.
	ClassSolidState
	// ClassRemovable is USB/SD flash: solid state, but a shallow controller
	// queue that stalls when it is filled.
	ClassRemovable
	// ClassNetwork is NFS/SMB/WebDAV — latency-bound rather than
	// bandwidth-bound, so requests in flight hide round trips.
	ClassNetwork
)

func (c Class) String() string {
	switch c {
	case ClassRotational:
		return "rotational"
	case ClassSolidState:
		return "solid-state"
	case ClassRemovable:
		return "removable"
	case ClassNetwork:
		return "network"
	default:
		return "unknown"
	}
}

// ClassForPath reports how the volume containing path behaves under concurrent
// reads. Best-effort, matching ForPath: an unresolvable class yields
// ClassUnknown rather than an error, because the class tunes a read strategy
// and is never a scan precondition
func ClassForPath(path string) Class {
	class, err := classForPath(path)
	if err != nil {
		return ClassUnknown
	}
	return class
}

// networkFilesystems are the fstype names that mean "the backing device is
// somewhere else, and unknowable from here". Decided before anything else,
// because a network mount's own storage is irrelevant to how it should be read
var networkFilesystems = map[string]bool{
	"nfs": true, "nfs4": true, "cifs": true, "smbfs": true, "smb3": true,
	"afpfs": true, "webdav": true, "davfs": true, "ftp": true,
	"fuse.sshfs": true, "osxfuse": true, "macfuse": true,
}
