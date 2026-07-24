// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jammutkarsh/wandersort/pkg/config"
)

func (a *App) newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Open the global config file",
		Long: `Opens ~/.wandersort/config.yaml in $EDITOR — output-path, workers, debug,
group-by, and home/work anchors all live there, applying to every scan/serve
unless overridden by a flag or environment variable. Creates the file with
explanatory comments first if it doesn't exist yet.

Prints the file to stdout instead of opening an editor when --print is given,
when stdout is not a terminal (piped or redirected), or when $EDITOR is unset.`,
		Example: `# Edit it
wandersort config

# Print it
wandersort config --print
wandersort config | grep group-by`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runConfig()
		},
	}

	cmd.Flags().BoolP(flagPrint, "p", false, "Print the config file instead of opening an editor")
	return cmd
}

func (a *App) runConfig() error {
	path, err := config.EnsureGlobalConfigFile()
	if err != nil {
		return fmt.Errorf("global config: %w", err)
	}

	// launching an editor is only sane on a terminal we own: piping or
	// redirecting means the caller wants the contents, not a full-screen vim
	// fighting over the same stdout
	piped := !term.IsTerminal(int(os.Stdout.Fd()))
	editor := os.Getenv("EDITOR")
	if v.GetBool(flagPrint) || piped || editor == "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read config: %w", err)
		}
		if editor == "" && !piped && !v.GetBool(flagPrint) {
			fmt.Fprintf(os.Stderr, "$EDITOR not set — printing %s:\n\n", path)
		}
		fmt.Print(string(data))
		return nil
	}

	cmd := exec.Command(editor, path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", editor, err)
	}
	return nil
}
