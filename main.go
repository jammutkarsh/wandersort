package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/jammutkarsh/wandersort/internal/cli"
	"github.com/jammutkarsh/wandersort/pkg/config"
)

func main() {
	cfg, err := config.Defaults()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	// Logger is built in the root command's PersistentPreRunE, once flags/env are resolved.
	app := &cli.App{Config: cfg}

	if err := app.Execute(); err != nil {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
		fmt.Fprintln(os.Stderr, errStyle.Render("Error:"), err)
		os.Exit(1)
	}
}
