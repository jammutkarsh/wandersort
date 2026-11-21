//go:build !darwin && !linux

package atomicfile

import "os"

// ponytail: no cache bypass here, so a read-back may come from memory and
// only proves the bytes reached the OS; FILE_FLAG_NO_BUFFERING (sector-aligned
// reads) would make it reach the drive on Windows.
func dropCached(*os.File) {}
