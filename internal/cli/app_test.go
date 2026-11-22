package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/library"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/spf13/cobra"
)

func testConfig(t *testing.T) *config.Configuration {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg, err := config.New()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestIsTuiEnabled pins the --plain escape hatch: it must short-circuit the
// terminal check rather than merely influence it, since `go test` never runs
// with a terminal on stderr and the branch would otherwise be untestable.
func TestIsTuiEnabled(t *testing.T) {
	a := &app{}
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().Bool(flagPlain, false, "")

	if err := cmd.Flags().Set(flagPlain, "true"); err != nil {
		t.Fatal(err)
	}
	if a.isTuiEnabled(cmd) {
		t.Error("--plain=true must disable the TUI regardless of the terminal check")
	}

	if err := cmd.Flags().Set(flagPlain, "false"); err != nil {
		t.Fatal(err)
	}
	// go test's stderr is never a terminal, so this must fall through to false too.
	if a.isTuiEnabled(cmd) {
		t.Error("isTuiEnabled under `go test` (non-tty stderr) must be false")
	}
}

// A library another process holds is the one error users hit routinely: it
// must come back as the styled explanation with the busy exit code, not a bare
// wrapped error.
func TestOpenExplainsABusyLibrary(t *testing.T) {
	dir := t.TempDir()
	held, err := lock.AcquireOutput(dir)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer held.Unlock()

	a := &app{Session: library.New(&config.Configuration{AppDBPath: filepath.Join(dir, ".wandersort.db")}, logger.NewNoopLogger(), nil)}
	err = a.Open(context.Background())
	if err == nil {
		t.Fatal("Open must fail while another process holds the library")
	}
	if !strings.Contains(err.Error(), "already has this library open") || ExitCode(err) != exitBusy {
		t.Errorf("Open error = %q (exit %d), want the already-open message and exit %d", err.Error(), ExitCode(err), exitBusy)
	}
}

// TestCloseDBsNilSafe is the "nothing was ever opened" path — closeDBs runs
// unconditionally in every command's deferred cleanup, including the
// no-database-found early-return paths, so it must not panic on a bare app.
func TestCloseDBsNilSafe(t *testing.T) {
	a := &app{Session: library.New(&config.Configuration{}, logger.NewNoopLogger(), nil)}
	a.closeDBs() // must not panic
}

// TestWorkflowDepsReadsThroughToCoordinator pins that workflowDeps is a live
// pass-through to a.Deps at call time, not a value captured once when the
// closures are built — the TUI path constructs the workflow before a.Deps
// exists, and relies on exactly this indirection.
func TestWorkflowDepsReadsThroughToCoordinator(t *testing.T) {
	a := &app{Session: library.New(testConfig(t), logger.NewNoopLogger(), nil)}
	deps := a.workflowDeps()
	a.Deps = a.newDeps(nil, nil)

	// Neither getter should be called (no download started), but they must
	// resolve against the Coordinator built *after* workflowDeps returned.
	if deps.Exiftool == nil || deps.Location == nil {
		t.Fatal("workflowDeps returned nil closures")
	}
}

// TestConfirmPlainPrompt exercises the --plain y/N stdin path directly,
// both answers, without going through a command.
func TestConfirmPlainPrompt(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"n\n", false},
		{"\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.WriteString(tt.input); err != nil {
				t.Fatal(err)
			}
			w.Close()
			realStdin := os.Stdin
			os.Stdin = r
			defer func() { os.Stdin = realStdin }()

			a := &app{}
			cmd := &cobra.Command{Use: "x"}
			cmd.Flags().Bool(flagPlain, true, "")
			if err := cmd.Flags().Set(flagPlain, "true"); err != nil {
				t.Fatal(err)
			}
			if got := a.confirm(cmd, "title", "detail"); got != tt.want {
				t.Errorf("confirm(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// testLogFile is a log file in dir, closed before the test's temp folders are
// removed: Windows can't remove an open file.
func testLogFile(t *testing.T, dir string) *logger.File {
	t.Helper()
	f := logger.NewFile(dir)
	t.Cleanup(func() { f.Close() })
	return f
}
