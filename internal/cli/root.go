package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/internal/api/admin"
	"github.com/jammutkarsh/wandersort/internal/api/pipeline"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
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
)

type App struct {
	Config           *config.Configuration
	Log              logger.Logger
	ExiftoolPath     string
	AppDB            *db.DB
	LocationDB       *db.DB
	LocationResolver *location.Resolver
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

func (a *App) InitExiftool() error {
	if a.ExiftoolPath != "" {
		return nil
	}
	exiftoolPath, err := exiftool.Check(a.Log, a.Config.ExecutablePath)
	if err != nil {
		return fmt.Errorf("exiftool not found — run 'wandersort setup' first: %w", err)
	}
	a.ExiftoolPath = exiftoolPath
	return nil
}

func (a *App) Close() {
	a.Log.Info("Closing databases")
	if a.AppDB != nil {
		a.AppDB.Close()
	}
	if a.LocationDB != nil {
		a.LocationDB.Close()
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
	// Bind the only hyphenated flag explicitly; AutomaticEnv covers the rest,
	// whose env names already match their uppercased flag (WORKERS, PORT, ...).
	v.BindEnv(flagOutputPath, "OUTPUT_PATH")
	v.AutomaticEnv()
}

func (a *App) Execute() error {
	return a.newRootCmd().Execute()
}

func (a *App) newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "wandersort",
		Short: "Find and organize duplicate media files",
		Long: `WanderSort scans your photo and video libraries and identifies duplicates by 
computing unique content hashes.

Environment Variables:
All CLI flags can be configured via environment variables. Simply use uppercase 
names and convert hyphens to underscores (e.g., --output-path becomes OUTPUT_PATH, 
--workers becomes WORKERS).`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := v.BindPFlags(cmd.Flags()); err != nil {
				return err
			}
			a.applyOverrides()
			// Build logger after overrides so --debug and --output-path take effect
			a.Log = logger.New(a.Config.LogLevel, a.Config.LogConsole, a.Config.LogFile)
			return nil
		},
	}

	rootCmd.PersistentFlags().StringP(flagOutputPath, "o", "", "Output directory (DB and logs)")
	rootCmd.PersistentFlags().Bool(flagDebug, false, "Enable debug logging")

	rootCmd.AddCommand(a.newSetupCmd())
	rootCmd.AddCommand(a.newScanCmd())
	rootCmd.AddCommand(a.newServeCmd())
	rootCmd.AddCommand(a.newReportCmd())
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

// applyOverrides layers ENV and CLI flag values over the config defaults.
// Precedence: flag > env > default (viper resolves flag/env; defaults come from config.Defaults).
func (a *App) applyOverrides() {
	if outputPath := v.GetString(flagOutputPath); outputPath != "" {
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
}
