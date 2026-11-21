package atomicfile

import (
	"os"

	"golang.org/x/sys/unix"
)

// dropCached evicts f's synced (clean) pages, so a read after it comes from
// the drive. darwin has no fadvise: invalidating a mapping of the file drops
// its cached pages instead, and the writes before it keep the cache's
// write-behind. Best effort: a refused hint only means the read may be served
// from memory.
func dropCached(f *os.File) {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return
	}
	mapped, err := unix.Mmap(int(f.Fd()), 0, int(info.Size()), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return
	}
	_ = unix.Msync(mapped, unix.MS_INVALIDATE)
	_ = unix.Munmap(mapped)
}
