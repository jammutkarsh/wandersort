//go:build windows

package exiftool

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processTree is pid and every process started under it.
func processTree(t *testing.T, pid int) []int {
	t.Helper()
	all, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(all)
	children := map[int][]int{}
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(all, &entry); err == nil; err = windows.Process32Next(all, &entry) {
		parent := int(entry.ParentProcessID)
		children[parent] = append(children[parent], int(entry.ProcessID))
	}
	tree := []int{pid}
	for i := 0; i < len(tree); i++ {
		tree = append(tree, children[tree[i]]...)
	}
	return tree
}
