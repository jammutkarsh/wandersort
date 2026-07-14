package main

import (
	"fmt"
	"os"

	"github.com/jammutkarsh/wandersort/internal/cli"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/style"
)

func main() {
	cfg, err := config.Defaults()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	app := &cli.App{Config: cfg}

	if err := app.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, style.Err.Render("Error:"), err)
		os.Exit(1)
	}
}
