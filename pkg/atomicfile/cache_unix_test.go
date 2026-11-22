//go:build darwin || linux

package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// A copy's read-back proves what the drive holds only if the copy's pages are
// gone from memory by then.
func TestCopyLeavesNoPagesCachedForTheReadBack(t *testing.T) {
	dir := t.TempDir()
	src, dest := filepath.Join(dir, "src"), filepath.Join(dir, "dest")
	const size = 4 << 20
	write(t, src, strings.Repeat("x", size))

	var resident int
	if _, err := Copy(t.Context(), src, dest, nil, func(tmp string) error {
		resident = residentPages(t, tmp, size)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if resident != 0 {
		t.Errorf("%d pages of the copy still cached before its read-back, want 0", resident)
	}
}

// residentPages counts p's pages held in memory, without reading any in.
func residentPages(t *testing.T, p string, size int) int {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	mapped, err := unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Munmap(mapped)
	pages := make([]byte, (size+os.Getpagesize()-1)/os.Getpagesize())
	if _, _, errno := unix.Syscall(unix.SYS_MINCORE, uintptr(unsafe.Pointer(&mapped[0])), uintptr(size), uintptr(unsafe.Pointer(&pages[0]))); errno != 0 {
		t.Skipf("mincore unavailable: %v", errno)
	}
	n := 0
	for _, v := range pages {
		n += int(v & 1)
	}
	return n
}
