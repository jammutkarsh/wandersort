package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type app struct {
	Config *config.Configuration
	Log    logger.Logger
	AppDB  *db.DB
	Deps   *install.Coordinator
	// outLock is the output-dir lock, taken with the database by openLibrary
	// and released with it by closeDBs.
	outLock *lock.Lock
	// logFile is this process's log, shared by the startup and TUI loggers.
	// Buffered in memory until openLibrary (or a warning) persists it.
	logFile *logger.File

	// jsonOut is --json: plain output, plus one result object on stdout;
	// jsonPrinted makes sure it is only one
	jsonOut, jsonPrinted bool
	// work is background work on the library; shutdown waits for it before
	// closing the database.
	work workGroup
}

// workGroup tracks background work so shutdown can wait for it; work that
// would start after shutdown began is refused instead.
type workGroup struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// start registers one piece of work, or reports false once shutdown began.
func (g *workGroup) start() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Add(1)
	return true
}

func (g *workGroup) done() { g.wg.Done() }

// closeAndWait refuses new work and waits for what is running.
func (g *workGroup) closeAndWait() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	g.wg.Wait()
}

func Execute() error {
	a := &app{}
	err := a.newRootCmd().Execute()
	// a failure before the command printed its own result still gets one
	return a.emitJSON(&jsonOutcome{}, time.Time{}, err)
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

// newDeps builds a Coordinator wired to this app's config and log. Both
// callbacks may be nil (every non-TUI path): no progress, and a failed try is
// logged before the next.
func (a *app) newDeps(onProgress func(install.Progress), beforeRetry install.RetryFunc) *install.Coordinator {
	return install.New(install.Options{
		ExecutablePath: a.Config.ExecutablePath,
		LocationDBPath: a.Config.LocationDBPath,
		Log:            a.Log,
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

// lockOutput takes the exclusive output-dir lock and styles the "already
// running" error.
func (a *app) lockOutput() (*lock.Lock, error) {
	l, err := lock.AcquireOutput(filepath.Dir(a.Config.AppDBPath))
	var running *lock.AlreadyRunningError
	if errors.As(err, &running) {
		return nil, withCode(exitBusy, fmt.Errorf("%s", tui.Bad.Render(fmt.Sprintf("Another wandersort process is already running (PID %d).", running.PID))+"\n\n"+
			tui.FaintTxt.Render("Only one scan or review can use the same output directory at a time.")+"\n"+
			tui.FaintTxt.Render("Stop the other process, then try again.")))
	}
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	return l, nil
}

// openLibrary opens the output folder as a library, once per session: check it
// may be one, take the output lock, open the database, load its settings, then
// remember it. In that order, a refused folder or a lost lock race writes
// nothing into the folder.
func (a *app) openLibrary(ctx context.Context) error {
	if a.AppDB != nil {
		return nil
	}
	// From here the run touches (or tried to touch) user data: keep its log.
	a.logFile.Persist()
	outputDir := a.Config.OutputDir()
	if err := config.CheckLibrary(outputDir); err != nil {
		return err
	}
	l, err := a.lockOutput()
	if err != nil {
		return err
	}
	appDB, err := db.New(ctx, a.Config.AppDBPath, a.Log)
	if err != nil {
		l.Unlock()
		return fmt.Errorf("app db: %w", err)
	}
	// The library's own settings (defaults if never saved). Read before
	// publishing the handle, so a failure leaves the library unopened.
	settings, err := config.LoadSettings(ctx, appDB)
	if err != nil {
		appDB.Close()
		l.Unlock()
		return err
	}
	a.AppDB, a.outLock = appDB, l
	a.Config.Settings = settings
	// only a real library goes in the recent list
	if err := a.Config.Remember(outputDir); err != nil {
		a.Log.Warn("could not record this library as recently used", "error", err)
	}
	return nil
}

// saveSettings writes the wizard's settings into their library, opening it
// first if needed; a settings-first session picks its output folder here.
func (a *app) saveSettings(ctx context.Context, outputDir string, s config.Settings) error {
	if a.AppDB == nil {
		a.Config.SetOutput(outputDir)
		if err := a.openLibrary(ctx); err != nil {
			return err
		}
	}
	if err := config.SaveSettings(ctx, a.AppDB, s); err != nil {
		return err
	}
	a.Config.Settings = s
	return nil
}

func (a *app) closeDBs() {
	// a failed Close can leave WAL/SHM locked (locking_mode=EXCLUSIVE); log it
	if a.AppDB != nil {
		if err := a.AppDB.Writer.Flush(); err != nil {
			a.Log.Error("some writes were lost before closing", "error", err)
		}
		a.Log.Info("Closing databases")
		if err := a.AppDB.Close(); err != nil {
			a.Log.Error("failed to close app database", "error", err)
		}
	}
	a.outLock.Unlock() // nil-safe; after Close, so no other process opens the database mid-close
	if a.Deps != nil {
		if err := a.Deps.Close(); err != nil {
			a.Log.Error("failed to close location database", "error", err)
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
