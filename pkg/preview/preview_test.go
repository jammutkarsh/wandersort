package preview

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// useRoot points the preview root at a directory only this test uses.
func useRoot(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp) // Windows reads the temp folder from TMP, then TEMP
	t.Setenv("TEMP", tmp)
	if err := os.MkdirAll(Root("lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Root("lib")
}

func TestMakeRoom(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// TestMakeRoomEvictsLeastRecentlyUsedFirst covers the 5%-of-disk budget:
		// when a new preview doesn't fit, the copies opened longest ago go until it
		// does. peekCmd touches a copy it reuses, so mtime is the LRU order.
		{"MakeRoomEvictsLeastRecentlyUsedFirst", func(t *testing.T) {
			root := useRoot(t)
			var dirs []string
			for i, name := range []string{"coldest", "warmer", "warmest"} {
				dir := filepath.Join(root, name)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, 1024), 0o644); err != nil {
					t.Fatal(err)
				}
				stamp := time.Now().Add(time.Duration(i-3) * time.Hour)
				if err := os.Chtimes(dir, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				dirs = append(dirs, dir)
			}

			// free is stated, so the budget is exact: room for four copies, of
			// which the incoming one needs three — the two oldest must go.
			const free = 4*1024*previewBudgetDivisor - 3*1024
			if err := makeRoom(root, 3*1024, free); err != nil {
				t.Fatalf("makeRoom: %v", err)
			}

			for _, dir := range dirs[:2] {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Errorf("%s survived, want the least recently used copies evicted first", dir)
				}
			}
			if _, err := os.Stat(dirs[2]); err != nil {
				t.Errorf("most recently used copy evicted unnecessarily: %v", err)
			}
		}},
		// TestMakeRoomRefusesAnOversizedPreview covers the other end: no amount of
		// eviction makes a preview bigger than the whole budget fit.
		{"MakeRoomRefusesAnOversizedPreview", func(t *testing.T) {
			if err := makeRoom(useRoot(t), math.MaxInt64/2, 1024); err == nil {
				t.Error("expected an over-budget preview to be refused")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

// Cleaning one library's copies leaves another library's alone.
func TestCleanKeepsOtherLibraries(t *testing.T) {
	useRoot(t)
	if err := os.MkdirAll(Root("other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Clean("lib"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Root("lib")); !os.IsNotExist(err) {
		t.Errorf("cleaned library's copies still there: %v", err)
	}
	if _, err := os.Stat(Root("other")); err != nil {
		t.Errorf("another library's copies went too: %v", err)
	}
}
