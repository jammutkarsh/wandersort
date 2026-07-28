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
	flagGeek       = "geek"
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
			if _, err := config.EnsureGlobalConfigFile(); err != nil {
				return fmt.Errorf("global config: %w", err)
			}
			cfg, warning, err := config.Resolve(flagOverridesFrom(cmd))
			if err != nil {
				return err
			}
			a.Config = cfg
			// Build logger after Resolve so --output-path takes effect
			a.Log = logger.New(a.Config.LogLevel, a.Config.LogConsole, a.Config.LogFile)
			if warning != "" {
				a.Log.Warn(warning, logger.UserKey, true)
			}
			if geek, _ := cmd.Flags().GetBool(flagGeek); geek {
				if err := a.startProfiler(); err != nil {
					return err
				}
			}
			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			a.stopProfiler()
			return nil
		},
	}

	rootCmd.PersistentFlags().StringP(flagOutputPath, "o", "", "Output directory (DB and logs)")
	rootCmd.PersistentFlags().Bool(flagPlain, false, "Disable the full-screen TUI; use plain line logging")
	rootCmd.PersistentFlags().Bool(flagGeek, false, "Enable CPU/memory profiling (writes .prof files to output dir)")

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

// flagOverridesFrom reads the config-affecting persistent flags off cmd into
// a config.FlagOverrides, leaving a field nil when the flag was never passed
// — Resolve uses nil to mean "fall through to env/file/default," which a
// flag's own zero value (empty string, 0) can't express on its own once
// --workers=0 or similar becomes a real (if odd) thing to type.
func flagOverridesFrom(cmd *cobra.Command) config.FlagOverrides {
	var overrides config.FlagOverrides
	flags := cmd.Flags()

	if flags.Changed(flagOutputPath) {
		if s, err := flags.GetString(flagOutputPath); err == nil {
			overrides.OutputPath = &s
		}
	}
	if flags.Changed(flagWorkers) {
		if n, err := flags.GetInt(flagWorkers); err == nil {
			overrides.Workers = &n
		}
	}
	if flags.Changed(flagCollapse) {
		if b, err := flags.GetBool(flagCollapse); err == nil {
			overrides.CollapseLevels = &b
		}
	}
	if flags.Changed(flagHWDateOnly) {
		if b, err := flags.GetBool(flagHWDateOnly); err == nil {
			overrides.HomeWorkDateOnly = &b
		}
	}
	if flags.Changed(flagMergeDays) {
		if b, err := flags.GetBool(flagMergeDays); err == nil {
			overrides.MergeSameLocationDays = &b
		}
	}

	return overrides
}
