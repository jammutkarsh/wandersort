package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/library"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type app struct {
	// Session is the library this run works on: its configuration, log, the
	// run's log file, the open database and the work running on it. Nil until
	// the root command's PreRun builds it.
	*library.Session
	Deps *install.Coordinator
	// settingsToSave is a settings form's answer on its way to the library:
	// the form hands it over, the shell saves it off the UI goroutine
	settingsToSave *config.Settings
	// Version is the build main.go stamps via -ldflags; `wandersort --version` prints it
	Version string

	// jsonOut is --json: plain output, plus one result object on stdout;
	// jsonPrinted makes sure it is only one
	jsonOut, jsonPrinted bool
}

func Execute(version string) error {
	a := &app{Version: version}
	err := withRestoreHint(a.newRootCmd().Execute())
	defer func() { // after the result below, which may still log
		if a.Session != nil {
			a.LogFile().Close()
		}
	}()

	// a failure before the command printed its own result still gets one; a
	// bad flag or argument fails before PersistentPreRunE reads --json
	if err != nil && !a.jsonOut {
		a.jsonOut = slices.ContainsFunc(os.Args[1:], func(arg string) bool {
			return arg == "--"+flagJSON || arg == "--"+flagJSON+"=true"
		})
	}
	return a.emitJSON(&jsonOutcome{}, time.Time{}, err)
}

// withRestoreHint adds the command that restores a library found with only its
// backup; core errors name no command, since other front ends restore their way.
func withRestoreHint(err error) error {
	if !errors.Is(err, config.ErrBackupOnly) {
		return err
	}
	return fmt.Errorf("%w\nRun 'wandersort admin db --restore' to put the backup back", err)
}

// interruptible is the context for plain commands that touch the library. The
// first ctrl+c/SIGTERM cancels it so deferred closes still run; a second ends
// the process.
func interruptible() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx, stop
}

// newDeps builds a Coordinator for this app; nil callbacks mean no progress and a logged wait between tries.
func (a *app) newDeps(onProgress func(install.Progress), beforeRetry install.RetryFunc) *install.Coordinator {
	return install.New(install.Options{
		ExecutablePath: a.Configuration().ExecutablePath,
		LocationDBPath: a.Configuration().LocationDBPath,
		Log:            a.Log(),
		OnProgress:     onProgress,
		BeforeRetry:    beforeRetry,
	})
}

// workflowDeps gates each pipeline phase on its own dependency. Closures, not
// method values: a.Deps may not exist yet when the workflow is built.
func (a *app) workflowDeps() workflow.Deps {
	return workflow.Deps{
		Exiftool: func(ctx context.Context) (string, error) {
			return a.Deps.Exiftool(ctx)
		},
		Location: func(ctx context.Context) (*location.Resolver, error) {
			return a.Deps.Location(ctx)
		},
	}
}

// Open, SaveSettings and Restore are the Session's, with a library held
// elsewhere explained for the terminal; shadowing them leaves no unexplained
// way in. Switch is not shadowed: only the Settings tab calls it, and the
// tab words its failures with explain.Error.
func (a *app) Open(ctx context.Context) error {
	return explainBusy(a.Session.Open(ctx))
}

func (a *app) Restore(ctx context.Context) error {
	return explainBusy(a.Session.Restore(ctx))
}

func (a *app) SaveSettings(ctx context.Context, settings config.Settings) error {
	places := func(ctx context.Context) (*location.Resolver, error) { return a.Deps.Location(ctx) }
	return explainBusy(a.Session.SaveSettings(ctx, settings, places))
}

// shownSettings is what the settings screens show: a save on its way, else the library's.
func (a *app) shownSettings() config.Settings {
	if a.settingsToSave != nil {
		return *a.settingsToSave
	}
	return a.Settings()
}

// busyError words a library held by another process for the terminal, keeping the cause in the chain.
type busyError struct {
	cause   error
	pid     int
	program string
}

func (e *busyError) Error() string {
	return tui.Bad.Render(fmt.Sprintf("%s already has this library open (PID %d).", e.program, e.pid)) + "\n" +
		tui.FaintTxt.Render("Stop it, then try again.")
}

func (e *busyError) Unwrap() error { return e.cause }

// explainBusy turns a library held by another process into the styled message
// and busy exit code; any other error passes through.
func explainBusy(err error) error {
	running, ok := errors.AsType[*lock.AlreadyRunningError](err)
	if !ok {
		return err
	}
	program := running.Program
	if program == "" {
		program = "Another program"
	}
	return withCode(exitBusy, &busyError{cause: err, pid: running.PID, program: program})
}

// closeDBs stops the library's work and closes it, then the location
// database; safe when neither is open.
func (a *app) closeDBs() {
	a.Shutdown()
	if a.Deps != nil {
		if err := a.Deps.Close(); err != nil {
			a.Log().Error("failed to close location database", "error", err)
		}
	}
}

func (a *app) isTuiEnabled(cmd *cobra.Command) bool {
	if plain, _ := cmd.Flags().GetBool(flagPlain); plain || a.jsonOut {
		return false
	}
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// confirm asks a yes/no question before something irreversible: a themed
// dialog in the full-screen TUI, or a plain y/N prompt when --plain /
// non-interactive. Anything but an explicit yes is a no.
func (a *app) confirm(cmd *cobra.Command, title, detail string) bool {
	if !a.isTuiEnabled(cmd) {
		fmt.Fprint(os.Stderr, tui.Attn.Render(title)+" (y/N): ")
		input, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		return input == "y" || input == "yes"
	}
	ok := false
	prog := tea.NewProgram(tui.NewConfirmModel(title, detail, &ok), tea.WithAltScreen(), tea.WithOutput(os.Stderr))
	if _, err := prog.Run(); err != nil {
		return false
	}
	return ok
}
