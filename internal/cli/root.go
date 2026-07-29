// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"fmt"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/spf13/cobra"
)

const (
	// CLI flags
	flagOutputPath = "output-path"
	flagPaths      = "paths"
	flagWorkers    = "workers"
	flagYes        = "yes"
	flagVertical   = "vertical"
	flagRebuild    = "rebuild"
	flagPrint      = "print"
	flagCollapse   = "collapse-levels"
	flagPlain      = "plain"
	flagHWDateOnly = "home-work-date-only"
	flagMergeDays  = "merge-same-location-days"
)

// requireConfigured refuses to run the pipeline until `wandersort config` has
// been through once, or the caller has explicitly said where to work via
// --output-path/OUTPUT_PATH — flag > env > config file precedence applies
// here same as everywhere else (see config.Resolve), so an explicit
// --output-path is as good as having run the wizard for this invocation.
func requireConfigured(a *app) error {
	if a.Config.Configured {
		return nil
	}
	return fmt.Errorf("not configured yet — run 'wandersort config' first (or pass --output-path)")
}

func (a *app) newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "wandersort",
		Short: "Organize your media library and find duplicates",
		Long: `WanderSort is a local-first media organizer. Point it at your photo and
video directories and it scans them, fingerprints every file to find
duplicates, and scores copies to pick the best one to keep.

Start with 'wandersort config' — the settings wizard, and a prerequisite for
scanning. Then 'wandersort scan' processes your libraries, installing anything
it needs on first use.`,
		Example: `# Change the global settings
wandersort config

# Scan one or more directories for media and duplicates
wandersort scan --paths ~/Pictures,/Volumes/SD

# Review and confirm the proposed folder structure
wandersort review`,
		Annotations: map[string]string{
			"env": `All flags can also be set via environment variables, using the uppercased
flag name (--output-path becomes OUTPUT_PATH, --workers becomes WORKERS).
Flags take precedence over environment variables.`,
		},
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cfg, warning, err := config.Resolve(config.Overrides{
				OutputPath:            flagStr(cmd, flagOutputPath),
				Workers:               flagInt(cmd, flagWorkers),
				CollapseLevels:        flagBool(cmd, flagCollapse),
				HomeWorkDateOnly:      flagBool(cmd, flagHWDateOnly),
				MergeSameLocationDays: flagBool(cmd, flagMergeDays),
			})
			if err != nil {
				return err
			}
			a.Config = cfg
			// Build logger after Resolve so --output-path takes effect
			a.Log = logger.New(a.Config.LogLevel, a.Config.LogConsole, a.Config.LogFile)
			if warning != "" {
				a.Log.Warn(warning, logger.UserKey, true)
			}
			return nil
		},
	}

	rootCmd.PersistentFlags().StringP(flagOutputPath, "o", "", "Output directory (DB and logs)")
	rootCmd.PersistentFlags().Bool(flagPlain, false, "Disable the full-screen TUI; use plain line logging")

	rootCmd.AddCommand(a.newConfigCmd())
	rootCmd.AddCommand(a.newScanCmd())
	rootCmd.AddCommand(a.newReviewCmd())
	rootCmd.AddCommand(a.newIssueCmd())
	rootCmd.AddCommand(a.newResetCmd())

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

// flagStr returns the string flag value, or "" when unset.
func flagStr(cmd *cobra.Command, name string) string {
	if !cmd.Flags().Changed(name) {
		return ""
	}
	s, _ := cmd.Flags().GetString(name)
	return s
}

// flagInt returns the int flag value, or 0 when unset.
func flagInt(cmd *cobra.Command, name string) int {
	if !cmd.Flags().Changed(name) {
		return 0
	}
	n, _ := cmd.Flags().GetInt(name)
	return n
}

// flagBool returns the bool flag as a TriBool, or Unset when not passed.
func flagBool(cmd *cobra.Command, name string) config.TriBool {
	if !cmd.Flags().Changed(name) {
		return config.Unset
	}
	b, err := cmd.Flags().GetBool(name)
	if err != nil {
		return config.Unset
	}
	return config.BoolToTri(b)
}
