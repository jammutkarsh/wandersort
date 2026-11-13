//go:build windows

package volume

import "io/fs"

// Device is unknown on Windows: a drive mounted in a folder is labelled with
// that folder's volume.
//
// ponytail: folder-mounted drives on Windows read as part of the folder; use
// GetVolumePathName per folder if anyone mounts drives that way.
func Device(fs.FileInfo) (uint64, bool) { return 0, false }
