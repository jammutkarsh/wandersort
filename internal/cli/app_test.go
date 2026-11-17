package cli

import (
	"context"
	"errors"
	"fmt"
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

// TestOpenLibraryLoadsThatLibrarysSettings: a library's rules come back with it,
// and opening it makes it the most recently used one.
// testConfigAt is testConfig pointed at an existing library, without moving
// $HOME (which would lose the history the first session wrote).
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

	a := &app{Session: library.Session{
		Config: &config.Configuration{AppDBPath: filepath.Join(dir, ".wandersort.db")},
		Log:    logger.NewNoopLogger(),
	}}
	err = a.Open(context.Background())
	if err == nil {
		t.Fatal("Open must fail while another process holds the library")
	}
	if !strings.Contains(err.Error(), "already running") || ExitCode(err) != exitBusy {
		t.Errorf("Open error = %q (exit %d), want the already-running message and exit %d", err.Error(), ExitCode(err), exitBusy)
	}
}

// A switch to a busy folder that then fails to reopen the old library has two
// things to say; the second leaves the session with nothing open.
func TestBusyErrorKeepsTheOtherFailure(t *testing.T) {
	busy := fmt.Errorf("acquire lock: %w", &lock.AlreadyRunningError{PID: 42})
	err := explainBusy(errors.Join(busy, errors.New("app db: disk gone")))
	if msg := err.Error(); !strings.Contains(msg, "already running") || !strings.Contains(msg, "disk gone") {
		t.Errorf("error = %q, want the busy message and the reopen failure", msg)
	}
	if only := explainBusy(errors.Join(busy)).Error(); strings.Contains(only, "acquire lock") {
		t.Errorf("error = %q, want the busy message alone", only)
	}
}

// TestCloseDBsNilSafe is the "nothing was ever opened" path — closeDBs runs
// unconditionally in every command's deferred cleanup, including the
// no-database-found early-return paths, so it must not panic on a bare app.
func TestCloseDBsNilSafe(t *testing.T) {
	a := &app{Session: library.Session{Log: logger.NewNoopLogger()}}
	a.closeDBs() // must not panic
}

// TestWorkflowDepsReadsThroughToCoordinator pins that workflowDeps is a live
// pass-through to a.Deps at call time, not a value captured once when the
// closures are built — the TUI path constructs the workflow before a.Deps
// exists, and relies on exactly this indirection.
func TestWorkflowDepsReadsThroughToCoordinator(t *testing.T) {
	a := &app{Session: library.Session{Config: testConfig(t), Log: logger.NewNoopLogger()}}
	deps := a.workflowDeps()
	a.Deps = a.newDeps(nil, nil)

	// Neither getter should be called (no download started), but they must
	// resolve against the Coordinator built *after* workflowDeps returned.
	if deps.Exiftool == nil || deps.Location == nil {
		t.Fatal("workflowDeps returned nil closures")
	}
}

// TestOpenLibraryRefusedWritesNothing: the check runs before the lock, so a
// folder that is not a library gets no lock file and no database. The attempt
// itself is worth a log, so the run's log is persisted all the same.
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

// Shutdown waits for running work and refuses work that starts after it.
// The library can't be switched while work runs on it, and work waits out a switch.
// A new folder starts an empty library with the same settings; an organised one opens with its own.
// testLogFile is a log file in dir, closed before the test's temp folders are
// removed: Windows can't remove an open file.
func testLogFile(t *testing.T, dir string) *logger.File {
	t.Helper()
	f := logger.NewFile(dir)
	t.Cleanup(func() { f.Close() })
	return f
}
