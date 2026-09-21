// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/spf13/cobra"
)

const (
	// CLI flags
	flagOutputPath = "output-path"
	flagPaths      = "paths"
	flagYes        = "yes"
	flagVertical   = "vertical"
	flagForce      = "force"
	flagPrint      = "print"
	flagPlain      = "plain"
	flagRestore    = "restore"
	flagReset      = "reset"
	flagMove       = "move"
	flagDryRun     = "dry-run"
	flagFull       = "full"
)

func (a *app) newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "wandersort",
		Short: "Organize your media library and find duplicates",
		Long: `WanderSort is a local-first media organizer. Point it at your photo and
video folders and it fingerprints every file, works out which are duplicates,
and plans a folder tree you can read.

Two verbs do the work: 'add' puts files into the plan, 'organise' lets you
correct the plan and then moves the files. 'check' re-reads the library later
to prove nothing has rotted. Run bare 'wandersort' to do all of it on screen —
the first run asks for your settings, and ctrl+t switches between them after.`,
		Example: `# Do everything on screen
wandersort

# Add folders to the library's plan
wandersort add --paths ~/Pictures,/Volumes/SD

# Correct the plan, then copy the files in
wandersort organise

# Check the library is still what was recorded
wandersort check`,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runRoot(cmd)
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// The settings that shape folders live in the library's own
			// database and are read by openLibrary; the only thing decided
			// here is which library that is — this flag, or the last one
			// used (config.New).
			cfg, err := config.New()
			if err != nil {
				return err
			}
			if out := flagStr(cmd, flagOutputPath); out != "" {
				cfg.SetOutput(path.New().ExpandPath(out))
			}
			a.Config = cfg
			// Build the logger after the output folder is settled, so the
			// startup line can name it.
			a.logFile = logger.NewFile(a.Config.LogDir)
			a.Log = logger.New(a.Config.LogLevel, a.Config.LogConsole, a.logFile)
			// The log no longer sits in the library, so say which one this run is about.
			a.Log.Info("wandersort started", "command", cmd.CommandPath(), "output", a.Config.OutputDir())
			return nil
		},
	}

	rootCmd.PersistentFlags().StringP(flagOutputPath, "o", "", "Library folder: empty, or one WanderSort already organized")
	rootCmd.PersistentFlags().Bool(flagPlain, false, "Disable the full-screen TUI; use plain line logging")

	rootCmd.AddCommand(a.newAddCmd())
	rootCmd.AddCommand(a.newReviewCmd())
	rootCmd.AddCommand(a.newExecuteCmd())
	rootCmd.AddCommand(a.newCheckCmd())
	rootCmd.AddCommand(a.newAdminCmd())

	rootCmd.InitDefaultCompletionCmd()
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "completion" {
			cmd.Long = `Generate the autocompletion script for wandersort for the specified shell.
See each sub-command's help for details on how to use the generated script.

Where to ideally store the generated scripts:
  Bash:       /etc/bash_completion.d/wandersort  (or ~/.bash_completion)
  Zsh:        ~/.zsh/completions/_wandersort      (ensure directory is in $fpath)
  Fish:       ~/.config/fish/completions/wandersort.fish
  PowerShell: Profile directory (run $PROFILE to find it)`
		}
	}

	setCustomHelp(rootCmd)

	return rootCmd
}

func flagStr(cmd *cobra.Command, name string) string {
	if !cmd.Flags().Changed(name) {
		return ""
	}
	s, _ := cmd.Flags().GetString(name)
	return s
}
