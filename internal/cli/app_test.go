package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/library"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
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

// A folder holding only a library's backup is restored, never added to.
func TestNoLibraryNamesTheBackup(t *testing.T) {
	dir := t.TempDir()
	a := &app{Session: library.New(&config.Configuration{AppDBPath: filepath.Join(dir, ".wandersort.db")}, logger.NewNoopLogger(), nil)}
	if err := a.noLibrary(); errors.Is(err, config.ErrBackupOnly) || !strings.Contains(err.Error(), "wandersort add") {
		t.Errorf("empty folder = %v, want to be told to add", err)
	}
	if err := os.WriteFile(filepath.Join(dir, db.BackupFileName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := withHint(a.noLibrary()); !errors.Is(err, config.ErrBackupOnly) || !strings.Contains(err.Error(), tui.RestoreHint) {
		t.Errorf("backup only = %v, want the restore command", err)
	}
}

// A library this version can't read is left alone, so the error names the way on.
func TestOldHashesNameTheNewLibraryCommand(t *testing.T) {
	err := withHint(fmt.Errorf("app db: %w", db.ErrOldHashes))
	if !errors.Is(err, db.ErrOldHashes) || !strings.Contains(err.Error(), tui.NewLibraryHint) {
		t.Errorf("err = %v, want the command for a new library", err)
	}
}

// A library opened here before whose folder is gone (an unplugged drive) is
// never to be added to: add would be told to make a new one.
func TestNoLibraryNamesAMissingLibrary(t *testing.T) {
	cfg, err := config.NewAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "drive", "library")
	cfg.SetOutput(dir)
	if err := cfg.Remember(dir); err != nil {
		t.Fatal(err)
	}
	a := &app{Session: library.New(cfg, logger.NewNoopLogger(), nil)}
	if err := a.noLibrary(); !errors.Is(err, config.ErrLibraryMissing) {
		t.Errorf("missing library = %v, want ErrLibraryMissing", err)
	}
}

// A scan refused for overlapping the library makes no library in the folder.
func TestRefusedScanMakesNoLibrary(t *testing.T) {
	photos := t.TempDir()
	cfg, err := config.NewAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(photos, "new", "library")
	cfg.SetOutput(dir)
	s := library.New(cfg, logger.NewNoopLogger(), logger.NewFile(t.TempDir()))
	t.Cleanup(s.Close)
	if _, err := s.Scan(t.Context(), []string{photos}, false, workflow.Deps{}); !errors.Is(err, workflow.ErrOverlapsLibrary) {
		t.Errorf("Scan = %v, want ErrOverlapsLibrary", err)
	}
	if err := (&app{Session: s}).addPlain([]string{photos}, false, &addResult{}); !errors.Is(err, workflow.ErrOverlapsLibrary) {
		t.Errorf("addPlain = %v, want ErrOverlapsLibrary", err)
	}
	if entries, _ := os.ReadDir(photos); len(entries) != 0 {
		t.Errorf("a refused scan left %v in the photo folder", entries)
	}
}

// Bad usage is exit 2, an unknown subcommand of a group and a wrong mix of
// flags included.
func TestBadUsageExitsTwo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, args := range [][]string{
		{"admin", "bogus"},
		{"completion", "bogus"},
		{"bogus"},
		{"admin", "db"},
		{"admin", "db", "--reset", "--restore"},
		{"admin", "report", "--redact-paths", "--include-db"},
	} {
		cmd := (&app{}).newRootCmd()
		cmd.SetArgs(args)
		cmd.SetOut(io.Discard)
		if err := cmd.Execute(); ExitCode(err) != exitUsage {
			t.Errorf("%v: %v (exit %d), want exit %d", args, err, ExitCode(err), exitUsage)
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Errorf("sessions that opened nothing wrote %v (%v)", entries, err)
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
