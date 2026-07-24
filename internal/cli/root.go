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
	flagDebug      = "debug"
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
	// Commands are extra subcommands registered on the root command, so
	// embedders and tests can extend the CLI without editing newRootCmd.
	Commands []*cobra.Command
	// InstallProgress, when set, receives dependency-download byte progress
	// (phase is "exiftool"/"location") so the setup TUI can draw a progress bar.
	// Deliberately not routed through the logger — per-byte ticks would flood
	// the file log. nil in every non-TUI path.
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
	if err := location.Setup(ctx, a.Log, a.Config.LocationDBPath, a.progressFor("location")); err != nil {
		return fmt.Errorf("location db: %w", err)
	}
	locationDB, err := db.New(ctx, a.Config.LocationDBPath, db.LocationDB, a.Log)
	if err != nil {
		return fmt.Errorf("location db: %w", err)
	}
	a.LocationDB = locationDB

	resolver, err := location.New(locationDB, a.Config.LocationDBPath, a.Log)
	if err != nil {
		return fmt.Errorf("location resolver: %w", err)
	}
	a.LocationResolver = resolver
	return nil
}

// installDir is the shared directory holding downloaded dependencies (exiftool
// binaries and the location database). It also hosts the install coordination lock.
func (a *App) installDir() string {
	return filepath.Dir(a.Config.LocationDBPath)
}

// EnsureDependencies installs the location database and exiftool if missing,
// then opens the location resolver. It holds the install lock for the duration
// so a concurrent scan/serve/setup never installs at the same time: callers
// wait for an in-progress install to finish, then continue (a no-op if it is
// already done).
func (a *App) EnsureDependencies(ctx context.Context) error {
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

var v = viper.New()

func init() {
	v.SetDefault(flagOutputPath, "")
	v.SetDefault(flagDebug, false)
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

// loadGlobalConfigFile ensures ~/.wandersort/config.yaml exists (writing the
// commented template the first time any command runs) and points viper at
// it, so its keys (output-path, workers, debug, rules, home-work, ...) layer
// between env and default (viper's normal config-file precedence: flag > env
// > config file > default). Every command creates it, not just `setup`/
// `config`, so the file — and its explanatory comments — is always there for
// a user to find and edit, not a thing they have to know to create first.
//
// A config file that doesn't parse is a warning, not a failure: YAML is easy
// to get subtly wrong (a stray tab, an unquoted colon) and refusing to run at
// all would mean a typo in an optional settings file bricks every command,
// including the `config` command that would let the user fix it. Every setting
// in the file is optional and has a default, so the run continues on defaults
// and says so. The returned string is the warning ("" when there is none) —
// it's logged once the logger exists, so it reaches the log file too, not just
// the terminal.
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

Run 'wandersort setup' once to install its dependencies, then
'wandersort scan' to process your libraries.`,
		Example: `# Install dependencies (run once)
wandersort setup

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
			// Build logger after overrides so --debug and --output-path take effect
			a.Log = logger.New(a.Config.LogLevel, a.Config.LogConsole, a.Config.LogFile)
			if configWarning != "" {
				a.Log.Warn(configWarning, logger.UserKey, true)
			}
			return nil
		},
	}

	rootCmd.PersistentFlags().StringP(flagOutputPath, "o", "", "Output directory (DB and logs)")
	rootCmd.PersistentFlags().Bool(flagDebug, false, "Enable debug logging")
	rootCmd.PersistentFlags().Bool(flagPlain, false, "Disable the full-screen TUI; use plain line logging")

	rootCmd.AddCommand(a.newSetupCmd())
	rootCmd.AddCommand(a.newConfigCmd())
	rootCmd.AddCommand(a.newScanCmd())
	rootCmd.AddCommand(a.newServeCmd())
	rootCmd.AddCommand(a.newReviewCmd())
	rootCmd.AddCommand(a.newReportCmd())
	rootCmd.AddCommand(a.newReportIssueCmd())
	rootCmd.AddCommand(a.newResetCmd())
	rootCmd.AddCommand(a.Commands...)

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

// applyOverrides layers ENV, config-file, and CLI flag values over the config
// defaults. Precedence: flag > env > config file (~/.wandersort/config.yaml,
// edit with `wandersort config`) > default — viper resolves flag/env/file;
// defaults come from config.Defaults(). output-path, workers, debug, and
// rules can all be set in the config file (see pkg/config/global.go's
// configTemplate); anchors are the one setting viper doesn't manage —
// they're read/written directly via pkg/config's Global struct.
func (a *App) applyOverrides() error {
	if outputPath := v.GetString(flagOutputPath); outputPath != "" {
		outputPath = path.New().ExpandPath(outputPath)
		a.Config.AppDBPath = filepath.Join(outputPath, config.DefaultDBFileName)
		a.Config.LogFile = filepath.Join(outputPath, config.DefaultLogFileName)
	}
	if workers := v.GetInt(flagWorkers); workers > 0 {
		a.Config.Workers = workers
	}
	if v.GetBool(flagDebug) {
		a.Config.LogLevel = "debug"
	}
	if port := v.GetString(flagPort); port != "" {
		a.Config.ServerPort = port
	}
	if groupBy := v.GetStringSlice(flagRules); len(groupBy) > 0 {
		for _, s := range groupBy {
			if s != groupByNone && !validRules[s] {
				return fmt.Errorf("invalid --rules value %q (want location, date, device, orientation, media, or none)", s)
			}
			// "none" means "no levels at all", so it can't sit alongside one.
			// vfs.ConfigFor only honours it as the sole value; mixed in, it
			// would silently drop out as an unrecognised level instead.
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
