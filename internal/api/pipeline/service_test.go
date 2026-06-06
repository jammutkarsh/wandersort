package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

func TestComputeScanPaths_FilterDuplicatePaths(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	grandChild := filepath.Join(child, "grand")
	if err := os.MkdirAll(grandChild, 0o755); err != nil {
		t.Fatal(err)
	}

	otherRoot := t.TempDir()

	svc := &Service{logger: logger.NewNoopLogger(), path: path.New()}

	resolvedRoot, err := svc.path.RealPath(root)
	resolvedOtherRoot, err := svc.path.RealPath(otherRoot)
	if err != nil {
		t.Fatal(err)
	}

	paths, err := svc.prepareScanRoots([]string{
		grandChild,
		root,
		child,
		root + string(filepath.Separator), // Duplicate with trailing separator
		otherRoot,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(paths) != 2 {
		t.Fatalf("%v", paths)
	}

	have := map[string]bool{}
	for _, p := range paths {
		have[p] = true
	}

	if !have[resolvedRoot] || !have[resolvedOtherRoot] {
		t.Fatalf("%v", paths)
	}
}

func TestPrepareScanRoots_ErrorOnNonexistentPath(t *testing.T) {
	svc := &Service{logger: logger.NewNoopLogger(), path: path.New()}

	_, err := svc.prepareScanRoots([]string{"/definitely/not/a/real/path"})
	if err == nil {
		t.Fatal(err)
	}
}

func TestPrepareScanRoots_ErrorOnFilePath(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "note.txt")
	if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	svc := &Service{logger: logger.NewNoopLogger(), path: path.New()}

	_, err := svc.prepareScanRoots([]string{file})
	if err == nil {
		t.Fatal(err)
	}
}
