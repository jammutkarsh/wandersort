package main

import (
	"fmt"
	"os"

	"github.com/jammutkarsh/wandersort/internal/cli"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// Set at build time via -ldflags (see .goreleaser.yaml).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	v := fmt.Sprintf("%s (commit %s, built %s)", version, commit, date)
	if err := cli.Execute(v); err != nil {
		fmt.Fprintln(os.Stderr, tui.Bad.Render("Error:"), err)
		os.Exit(cli.ExitCode(err))
	}
}
