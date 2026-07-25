// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/internal/api/admin"
	"github.com/jammutkarsh/wandersort/internal/api/pipeline"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/lock"
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
	flagRules      = "rules"
	flagRebuild    = "rebuild"
	flagPrint      = "print"
	flagCollapse   = "collapse-levels"
	flagPlain      = "plain"
	flagHWDateOnly = "home-work-date-only"
	flagMergeDays  = "merge-same-location-days"

	// groupByNone is the --rules sentinel for "no levels below Year/Month".
	// vfs owns its meaning (vfs.ConfigFor is the only thing that acts on it).
	groupByNone = vfs.RuleNone
)

// validRules are the recognized --rules tokens, beyond groupByNone
var validRules = map[string]bool{
	vfs.RuleLocation:    true,
	vfs.RuleDate:        true,
	vfs.RuleDevice:      true,
	vfs.RuleOrientation: true,
	vfs.RuleMedia:       true,
}

type App struct {
	Config           *config.Configuration
	Log              logger.Logger
	ExiftoolPath     string
	AppDB            *db.DB
	LocationDB       *db.DB
	LocationResolver *location.Resolver
	// InstallProgress, when set, receives dependency-download byte progress
	// (phase is "exiftool"/"location") so the install screen can draw a bar.
	// Not routed through the logger — per-byte ticks would flood the file log.
	// nil in every non-TUI path.
	InstallProgress func(phase string, done, total int64)
}

// progressFor binds InstallProgress to one install phase for a Setup callback,
// or returns nil when no TUI is listening (so the download skips the wrapper).
func (a *App) progressFor(phase string) func(done, total int64) {
	if a.InstallProgress == nil {
		return nil
	}
	return func(done, total int64) { a.InstallProgress(phase, done, total) }
}

func (a *App) InitAppDB(ctx context.Context) error {
	if a.AppDB != nil {
		return nil
	}
	appDB, err := db.New(ctx, a.Config.AppDBPath, db.AppDB, a.Log)
	if err != nil {
		return fmt.Errorf("app db: %w", err)
	}
	a.AppDB = appDB
	return nil
}

func (a *App) InitLocationResolver(ctx context.Context) error {
	if a.LocationResolver != nil {
		return nil
	}
	// Download the location database on first use so scan/serve work without a
	// separate setup step. No-op if it already exists.
	resolver, locationDB, err := location.Open(ctx, a.Log, a.Config.LocationDBPath, a.progressFor("location"))
	if err != nil {
		return err
	}
	a.LocationDB = locationDB
	a.LocationResolver = resolver
	return nil
}

// installDir is the shared directory holding downloaded dependencies (exiftool
// binaries and the location database). It also hosts the install coordination lock.
func (a *App) installDir() string {
	return filepath.Dir(a.Config.LocationDBPath)
}

// EnsureDependencies installs the location database and exiftool if missing,
// then opens the resolver. Holds the install lock throughout, so a concurrent
// scan/serve waits rather than installing at the same time.
func (a *App) EnsureDependencies(ctx context.Context) error {
	// try non-blocking first, so waiting can be announced rather than looking hung
	l, err := lock.AcquireInstall(ctx, a.installDir(), false)
	if errors.Is(err, lock.ErrHeld) {
		a.Log.Info("Waiting for another process to finish installing dependencies...", logger.UserKey, true)
		l, err = lock.AcquireInstall(ctx, a.installDir(), true)
	}
	if err != nil {
		return fmt.Errorf("wait for dependency install: %w", err)
	}
	defer l.Unlock()

	if err := a.InitLocationResolver(ctx); err != nil {
		return err
	}
	return a.InitExiftool(ctx)
}

func (a *App) InitExiftool(ctx context.Context) error {
	if a.ExiftoolPath != "" {
		return nil
	}
	// Install exiftool on first use so scan/serve work without a separate setup
	// step. No-op if a suitable version is already present.
	exiftoolPath, err := exiftool.Setup(ctx, a.Log, a.Config.ExecutablePath, a.progressFor("exiftool"))
	if err != nil {
		return fmt.Errorf("exiftool: %w", err)
	}
	a.ExiftoolPath = exiftoolPath
	return nil
}

func (a *App) Close() {
	a.Log.Info("Closing databases")
	// A failed Close can leave the WAL/SHM files locked (locking_mode=EXCLUSIVE),
	// preventing the next scan/serve from starting — always log the cause.
	if a.AppDB != nil {
		if err := a.AppDB.Close(); err != nil {
			a.Log.Error("failed to close app database", "error", err)
		}
	}
	if a.LocationDB != nil {
		if err := a.LocationDB.Close(); err != nil {
			a.Log.Error("failed to close location database", "error", err)
		}
	}
}

func (a *App) AdminService() *admin.Service {
	return admin.NewService(a.Log, admin.NewRepository(a.AppDB))
}

func (a *App) PipelineService(wf *workflow.Workflow) *pipeline.Service {
	return pipeline.NewService(a.Log, wf, pipeline.NewRepository(a.AppDB))
}

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
	v.SetDefault(flagRules, []string{})
	v.SetDefault(flagCollapse, true)
	v.SetDefault(flagPlain, false)
	v.SetDefault(flagHWDateOnly, true)
	v.SetDefault(flagMergeDays, true)
	// Bind the hyphenated flags explicitly; AutomaticEnv covers the rest,
	// whose env names already match their uppercased flag (WORKERS, PORT, ...).
	v.BindEnv(flagOutputPath, "OUTPUT_PATH")
	v.BindEnv(flagRules, "RULES")
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

func (a *App) Execute() error {
	return a.newRootCmd().Execute()
}

func (a *App) newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "wandersort",
		Short: "Organize your media library and find duplicates",
		Long: `WanderSort is a local-first media organizer. Point it at your photo and
video directories and it scans them, fingerprints every file to find
duplicates, and scores copies to pick the best one to keep.

Run 'wandersort scan' to process your libraries — it installs anything it
needs on first use. 'wandersort config' opens the settings wizard.`,
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
	rootCmd.AddCommand(a.newReportIssueCmd())
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
// read here through viper, except home-work, which anchor.go loads directly.
func (a *App) applyOverrides() error {
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
	if groupBy := v.GetStringSlice(flagRules); len(groupBy) > 0 {
		for _, s := range groupBy {
			if s != groupByNone && !validRules[s] {
				return fmt.Errorf("invalid --rules value %q (want location, date, device, orientation, media, or none)", s)
			}
			// ConfigFor honours "none" only as the sole value; mixed in it
			// would silently drop out as an unrecognised level
			if s == groupByNone && len(groupBy) > 1 {
				return fmt.Errorf("invalid --rules %v: %q cannot be combined with other levels", groupBy, groupByNone)
			}
		}
		a.Config.Rules = groupBy
	}
	// no CLI flag — config file and env only, so it has no per-command default
	// to layer over; viper's own default (true) is the fallback
	a.Config.CollapseLevels = v.GetBool(flagCollapse)
	a.Config.HomeWorkDateOnly = v.GetBool(flagHWDateOnly)
	a.Config.MergeSameLocationDays = v.GetBool(flagMergeDays)
	return nil
}
