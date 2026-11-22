//go:build !windows

package workflow

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/path"
)

// A folder the walk can't open is named in the result: nothing in it was
// added, and a card wiped on the word of a clean run would take it along.
func TestScanReportsWhatItCouldNotOpen(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens a folder whatever its permissions")
	}
	root := t.TempDir()
	shut := filepath.Join(root, "shut")
	if err := os.Mkdir(shut, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(shut, 0o700) })
	real, err := path.New().RealPath(root)
	if err != nil {
		t.Fatal(err)
	}

	wf, _ := newTestWorkflow(t)
	res := Result{Roots: []string{real}}
	for _, phase := range wf.workflowPhases(&res, false) {
		if phase.kind == workflowPhaseScan {
			if _, err := phase.run(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	}
	if want := []string{filepath.Join(real, "shut")}; !slices.Equal(res.Unseen, want) {
		t.Errorf("Unseen = %v, want %v", res.Unseen, want)
	}
}
