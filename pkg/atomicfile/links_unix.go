//go:build !windows

package atomicfile

import (
	"io/fs"
	"syscall"
)

// sharedInode reports whether fi's file has more than one name, so removing
// one of them cannot delete it.
func sharedInode(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && uint64(st.Nlink) >= 2
}
