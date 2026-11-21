package atomicfile

import (
	"os"

	"golang.org/x/sys/unix"
)

// keepOutOfCache stops f's writes staying in the page cache, so a read after
// the sync comes from the drive. Best effort: a refused hint only means the
// read may be served from memory.
func keepOutOfCache(f *os.File) { _, _ = unix.FcntlInt(f.Fd(), unix.F_NOCACHE, 1) }

// dropCached is a no-op here: keepOutOfCache already kept the pages out.
func dropCached(*os.File) {}
