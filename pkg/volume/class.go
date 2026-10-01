package volume

import (
	"os"
	"path/filepath"
)

// Class is how a volume behaves under concurrent reads. Detection is a best
// guess (RAIDs mix, NASes hide their disks); treat ClassUnknown as a
// conservative answer, not a failure.
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

// ClassForPath reports the class of the volume containing path, ClassUnknown
// when it can't tell.
func ClassForPath(path string) Class {
	class, err := classForPath(path)
	if err != nil {
		return ClassUnknown
	}
	return class
}

// IsNetwork reports whether path, or its nearest existing ancestor, is on a
// network mount (NFS, SMB, …). False when it can't tell.
func IsNetwork(path string) bool {
	for {
		if _, err := os.Stat(path); err == nil {
			return ClassForPath(path) == ClassNetwork
		}
		parent := filepath.Dir(path)
		if parent == path {
			return false
		}
		path = parent
	}
}

// networkFilesystems are fstypes whose backing storage is unknowable here;
// checked first.
var networkFilesystems = map[string]bool{
	"nfs": true, "nfs4": true, "cifs": true, "smbfs": true, "smb3": true,
	"afpfs": true, "webdav": true, "davfs": true, "ftp": true,
	"fuse.sshfs": true, "osxfuse": true, "macfuse": true,
}
