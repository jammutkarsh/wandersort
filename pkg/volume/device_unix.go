//go:build unix

package volume

import (
	"io/fs"
	"syscall"
)

// Device returns the ID of the device holding info's file: a folder on a
// different device from its parent is where another drive is mounted.
func Device(info fs.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}
