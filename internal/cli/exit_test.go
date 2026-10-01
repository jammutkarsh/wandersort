package cli

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/lock"
)

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"success", nil, exitOK},
		{"plain error", errors.New("boom"), exitError},
		{"wrapped partial", fmt.Errorf("copy: %w", withCode(exitPartial, errors.New("2 failed"))), exitPartial},
		{"usage", withCode(exitUsage, errors.New("bad flag")), exitUsage},
		{"lock held anywhere in the chain", fmt.Errorf("open: %w", &lock.AlreadyRunningError{PID: 7}), exitBusy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
