// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/logger"
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
	flagCollapse   = "collapse-levels"
	flagPlain      = "plain"
	flagSPDateOnly = "saved-places-date-only"
	flagMergeDays  = "merge-same-location-days"
	flagDB         = "db"
	flagCopy       = "copy"
	flagMove       = "move"
	flagDryRun     = "dry-run"
)

func (a *app) newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "wandersort",
		Short: "Organize your media library and find duplicates",
		Long: `WanderSort is a local-first media organizer. Point it at your photo and
video directories and it scans them, fingerprints every file to find
duplicates, and scores copies to pick the best one to keep.

'wandersort scan' works on defaults right away. Run 'wandersort config' — the
settings wizard — any time to set your output folder, folder rules, and
saved places; saving re-proposes the folder structure from the new settings
right away, no re-scan needed.`,
		Example: `# Change the global settings
wandersort config

# Scan one or more directories for media and duplicates
wandersort scan --paths ~/Pictures,/Volumes/SD

# Review and confirm the proposed folder structure
wandersort review`,
		Annotations: map[string]string{
			"env": `All flags can also be set via environment variables, using the uppercased
flag name (--output-path becomes OUTPUT_PATH).
Flags take precedence over environment variables.`,
		},
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runRoot(cmd)
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Kept on the app: the shell re-resolves after its own settings
			// wizard rewrites config.yaml mid-session, and the flag layer must
			// still win over what was just written.
			a.overrides = config.Overrides{
				OutputPath:            flagStr(cmd, flagOutputPath),
				CollapseLevels:        flagBool(cmd, flagCollapse),
				SavedPlacesDateOnly:   flagBool(cmd, flagSPDateOnly),
				MergeSameLocationDays: flagBool(cmd, flagMergeDays),
			}
			cfg, warning, err := config.Resolve(a.overrides)
			if err != nil {
				return err
			}
			a.Config = cfg
			// Build logger after Resolve so --output-path takes effect
			a.logFile = logger.NewFile(a.Config.LogDir)
			a.Log = logger.New(a.Config.LogLevel, a.Config.LogConsole, a.logFile)
			// The log no longer sits in the library, so say which one this run is about.
			a.Log.Info("wandersort started", "command", cmd.CommandPath(), "output", filepath.Dir(a.Config.AppDBPath))
			if warning != "" {
				a.Log.Warn(warning, logger.UserKey, true)
			}
			return nil
		},
	}

	rootCmd.PersistentFlags().StringP(flagOutputPath, "o", "", "Library folder: empty, or one WanderSort already organized")
	rootCmd.PersistentFlags().Bool(flagPlain, false, "Disable the full-screen TUI; use plain line logging")

	rootCmd.AddCommand(a.newConfigCmd())
	rootCmd.AddCommand(a.newScanCmd())
	rootCmd.AddCommand(a.newReviewCmd())
	rootCmd.AddCommand(a.newExecuteCmd())
	rootCmd.AddCommand(a.newIssueCmd())
	rootCmd.AddCommand(a.newResetCmd())
	rootCmd.AddCommand(a.newRecoverCmd())

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
