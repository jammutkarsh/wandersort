package cli

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/lock"
)

// Exit codes a script can act on.
const (
	exitOK      = 0
	exitError   = 1 // stopped by an error; the next run carries on
	exitUsage   = 2 // can't start: bad flags or arguments
	exitPartial = 3 // finished, but some files failed
	exitBusy    = 4 // another wandersort holds the library
)

// codedError carries the exit code an error maps to.
type codedError struct {
	code int
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

func withCode(code int, err error) error { return &codedError{code: code, err: err} }

// ExitCode is the process exit code for an error Execute returned.
func ExitCode(err error) int {
	if err == nil {
		return exitOK
	}
	if ce, ok := errors.AsType[*codedError](err); ok {
		return ce.code
	}
	if _, ok := errors.AsType[*lock.AlreadyRunningError](err); ok {
		return exitBusy
	}
	return exitError
}

// usageError marks cobra's flag-parsing failures as exit 2.
func usageError(_ *cobra.Command, err error) error { return withCode(exitUsage, err) }

// noArgs refuses positional arguments, an unknown command among them, as exit 2.
func noArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return withCode(exitUsage, err)
	}
	return nil
}

// jsonOutcome is what every --json result carries.
type jsonOutcome struct {
	Library   string `json:"library"`
	ElapsedMS int64  `json:"elapsedMs"`
	Exit      int    `json:"exit"`
	Error     string `json:"error,omitempty"`
}

func (o *jsonOutcome) outcome() *jsonOutcome { return o }

// jsonResult is a command's --json result.
type jsonResult interface{ outcome() *jsonOutcome }

// emitJSON prints res with err's exit code once per process when --json is set, and returns err.
func (a *app) emitJSON(res jsonResult, start time.Time, err error) error {
	if !a.jsonOut || a.jsonPrinted {
		return err
	}
	o := res.outcome()
	if a.Config != nil {
		o.Library = a.Config.OutputDir()
	}
	if !start.IsZero() {
		o.ElapsedMS = time.Since(start).Milliseconds()
	}
	o.Exit = ExitCode(err)
	if err != nil {
		o.Error = ansi.Strip(err.Error())
	}
	a.jsonPrinted = true
	if werr := json.MarshalWrite(os.Stdout, res); werr != nil {
		return errors.Join(err, fmt.Errorf("write --json result: %w", werr))
	}
	fmt.Fprintln(os.Stdout)
	return err
}
