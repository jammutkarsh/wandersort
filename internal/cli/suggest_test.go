package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/path"
)

// Nothing below a library is offered as a completion, so tab can't lead into one.
func TestSuggestDirsStaysOutOfALibrary(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "lib")
	for _, d := range []string{filepath.Join(lib, "2025", "12_December"), filepath.Join(root, "other", "photos")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(lib, ".wandersort.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	paths := path.New()

	for _, typed := range []string{lib, filepath.Join(lib, "2025"), filepath.Join(lib, "20")} {
		if got := suggestDirs(paths, typed); len(got) != 0 {
			t.Errorf("suggestDirs(%q) = %v, want nothing from inside the library", typed, got)
		}
	}
	got := suggestDirs(paths, filepath.Join(root, "other"))
	if len(got) != 1 || !strings.HasSuffix(got[0], "photos") {
		t.Errorf("suggestDirs(other) = %v, want its photos folder", got)
	}
	if got := suggestDirs(paths, filepath.Join(root, "li")); len(got) != 1 || !strings.HasSuffix(got[0], "lib") {
		t.Errorf("the library folder itself is still offered, got %v", got)
	}
}
