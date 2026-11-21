package atomicfile

import (
	"os"

	"golang.org/x/sys/unix"
)

// dropCached evicts f's synced (clean) pages, so a read after it comes from
// the drive. Best effort: a refused hint only means the read may be served
// from memory.
func dropCached(f *os.File) { _ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED) }
