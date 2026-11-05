package tui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"testing"
)

func TestExplain(t *testing.T) {
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
	}
	for _, tt := range tests {
		if got := Explain(tt.err); got != tt.want {
			t.Errorf("%s: Explain = %q, want %q", tt.name, got, tt.want)
		}
	}
}
