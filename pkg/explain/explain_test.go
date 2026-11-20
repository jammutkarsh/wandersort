package explain

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"testing"
)

func TestError(t *testing.T) {
	denied := &fs.PathError{Op: "open", Path: "/x", Err: syscall.EACCES}
	_, missing := os.Open("/definitely/not/here")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"permission, wrapped", fmt.Errorf("scan: %w", denied), explanations[4].sentence},
		{"missing path", missing, explanations[5].sentence},
		{"full drive", fmt.Errorf("copy: %w", syscall.ENOSPC), explanations[2].sentence},
		{"already plain", errors.New("that folder is inside the library"), "that folder is inside the library"},
		// a known cause in one failure must not hide the others
		{
			"joined", errors.Join(fmt.Errorf("copy: %w", syscall.ENOSPC), errors.New("could not reopen")),
			explanations[2].sentence + "\ncould not reopen",
		},
		{
			"several %w stay whole", fmt.Errorf("%w: worker 2: %w", errors.New("exiftool stopped"), errors.New("broken pipe")),
			"exiftool stopped: worker 2: broken pipe",
		},
	}
	for _, tt := range tests {
		if got := Error(tt.err); got != tt.want {
			t.Errorf("%s: Error = %q, want %q", tt.name, got, tt.want)
		}
	}
}
