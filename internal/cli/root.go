// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"fmt"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	// CLI flags
	flagOutputPath = "output-path"
	flagPaths      = "paths"
	flagWorkers    = "workers"
	flagPort       = "port"
	flagYes        = "yes"
	flagVertical   = "vertical"
	flagRebuild    = "rebuild"
	flagPrint      = "print"
	flagCollapse   = "collapse-levels"
	flagPlain      = "plain"
	flagHWDateOnly = "home-work-date-only"
	flagMergeDays  = "merge-same-location-days"
)

// Global viper instance — standard cobra pattern. Flags, env vars, and the
// config file all layer through this single registry so precedence (flag >
// env > config file > default) applies consistently without threading a
// *viper.Viper through every cobra command.
var v = viper.New()

func init() {
	v.SetDefault(flagOutputPath, "")
	v.SetDefault(flagPort, "")
	v.SetDefault(flagPaths, []string{})
	v.SetDefault(flagWorkers, 0)
	v.SetDefault(flagYes, false)
	v.SetDefault(flagCollapse, true)
	v.SetDefault(flagPlain, false)
	v.SetDefault(flagHWDateOnly, true)
	v.SetDefault(flagMergeDays, true)
	// Bind the hyphenated flags explicitly; AutomaticEnv covers the rest,
	// whose env names already match their uppercased flag (WORKERS, PORT, ...).
	v.BindEnv(flagOutputPath, "OUTPUT_PATH")
	v.BindEnv(flagCollapse, "COLLAPSE_LEVELS")
	v.BindEnv(flagHWDateOnly, "HOME_WORK_DATE_ONLY")
	v.BindEnv(flagMergeDays, "MERGE_SAME_LOCATION_DAYS")
	v.AutomaticEnv()
}

// loadGlobalConfigFile ensures ~/.wandersort/config.yaml exists and points
// viper at it, so its keys layer between env and default. Returns the warning
// text for an unparseable file ("" when there is none) — the caller logs it
// once the logger exists, so it reaches the log file too.
//
// Unparseable is a warning, not a failure: the file is hand-editable and every
// setting in it is optional, so one stray tab must not brick every command —
// including `wandersort config`, the one that would fix it.
func loadGlobalConfigFile() (warning string, err error) {
	path, err := config.EnsureGlobalConfigFile()
	if err != nil {
		return "", fmt.Errorf("global config: %w", err)
	}
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		// viper only commits parsed values on success, so nothing half-read
		// leaks into the settings — flags, env, and defaults still apply.
		return fmt.Sprintf("Ignoring %s — it isn't valid YAML (%v). Using defaults; fix the file or delete it to get a fresh one.", path, err), nil
	}
	return "", nil
}

// requireConfigured refuses to run the pipeline until `wandersort config` has
// been through once, or the caller has explicitly said where to work via
// --output-path/OUTPUT_PATH — flag > env > config file precedence applies
// here same as everywhere else (see applyOverrides), so an explicit
// --output-path is as good as having run the wizard for this invocation. An
// untouched ~/.wandersort/config.yaml is empty (every command creates it,
// only the wizard fills it), which means no output path, no rules, and no
// home/work anchors — a proposal built from those defaults is one the user
// has to throw away and redo. output-path is the marker: the wizard always
// writes it and nothing else does (besides the flag/env checked here).
//
// An unreadable or unparseable file lands here too, and the message is the same
// on purpose — `wandersort config` is the fix for both.
func requireConfigured() error {
	if v.GetString(flagOutputPath) != "" {
		return nil
	}
	if g, err := config.LoadGlobal(); err == nil && g.OutputPath != "" {
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

# Show a summary of the last scan
wandersort report`,
		Annotations: map[string]string{
			"env": `All flags can also be set via environment variables, using the uppercased
flag name (--output-path becomes OUTPUT_PATH, --workers becomes WORKERS).
Flags take precedence over environment variables.`,
		},
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := v.BindPFlags(cmd.Flags()); err != nil {
				return err
			}
			configWarning, err := loadGlobalConfigFile()
			if err != nil {
				return err
			}
			if err := a.applyOverrides(); err != nil {
				return err
			}
			// Build logger after overrides so --output-path takes effect
			a.Log = logger.New(a.Config.LogLevel, a.Config.LogConsole, a.Config.LogFile)
			if configWarning != "" {
				a.Log.Warn(configWarning, logger.UserKey, true)
			}
			return nil
		},
	}

	rootCmd.PersistentFlags().StringP(flagOutputPath, "o", "", "Output directory (DB and logs)")
	rootCmd.PersistentFlags().Bool(flagPlain, false, "Disable the full-screen TUI; use plain line logging")

	rootCmd.AddCommand(a.newConfigCmd())
	rootCmd.AddCommand(a.newScanCmd())
	rootCmd.AddCommand(a.newServeCmd())
	rootCmd.AddCommand(a.newReviewCmd())
	rootCmd.AddCommand(a.newReportCmd())
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

// applyOverrides layers env, config-file and flag values over the defaults.
// Precedence: flag > env > config file > default. Every key in the file is
// read here through viper, except home-work and Rules, which are loaded
// directly from the file (see config.LoadGlobal) — no flag or env can set
// them, only `wandersort config`.
func (a *app) applyOverrides() error {
	if outputPath := v.GetString(flagOutputPath); outputPath != "" {
		outputPath = path.New().ExpandPath(outputPath)
		a.Config.AppDBPath = filepath.Join(outputPath, config.DefaultDBFileName)
		a.Config.LogFile = filepath.Join(outputPath, config.DefaultLogFileName)
	}
	if workers := v.GetInt(flagWorkers); workers > 0 {
		a.Config.Workers = workers
	}
	if port := v.GetString(flagPort); port != "" {
		a.Config.ServerPort = port
	}
	// Global config file failing to parse is already surfaced as a warning by
	// loadGlobalConfigFile; silently keep Rules at its default here too.
	if g, err := config.LoadGlobal(); err == nil && len(g.Rules) > 0 {
		a.Config.Rules = g.Rules
	}
	// no CLI flag — config file and env only, so it has no per-command default
	// to layer over; viper's own default (true) is the fallback
	a.Config.CollapseLevels = v.GetBool(flagCollapse)
	a.Config.HomeWorkDateOnly = v.GetBool(flagHWDateOnly)
	a.Config.MergeSameLocationDays = v.GetBool(flagMergeDays)
	return nil
}
