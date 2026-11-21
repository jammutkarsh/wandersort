//go:build unix

package scanner

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A pipe named like a photo would block the hash read forever.
func TestWalkRootSkipsPipes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "own.jpg"), []byte("photo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "pipe.jpg"), 0o644); err != nil {
		t.Skipf("pipes unavailable: %v", err)
	}
	sc := newTestScanner(t)
	filesChan := make(chan FileDiscovery, 10)
	if _, err := sc.walkRoot(context.Background(), root, "", filesChan); err != nil {
		t.Fatalf("walkRoot: %v", err)
	}
	close(filesChan)
	var names []string
	for d := range filesChan {
		names = append(names, d.Name)
	}
	if len(names) != 1 || names[0] != "own.jpg" {
		t.Errorf("discovered %v, want only own.jpg (the pipe skipped)", names)
	}
}
