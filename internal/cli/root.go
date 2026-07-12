package cli

import (
	"context"
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	flagOutputPath = "output-path"
	flagDebug      = "debug"
	flagPaths      = "paths"
	flagWorkers    = "workers"
	flagPort       = "port"
	flagYes        = "yes"
)

const rootLongDesc = `WanderSort scans your photo and video libraries, detects duplicates using
content hashing, and helps you keep the best copy.

All flags can also be set via environment variables (uppercase, hyphens to
underscores: OUTPUT_PATH, LOG_LEVEL, WORKERS, PORT).`

type App struct {
	Config           *config.Configuration
	Log              logger.Logger
	ExiftoolPath     string
	AppDB            *db.DB
	LocationDB       *db.DB
	LocationResolver *location.Resolver
	Bootstrap        func(ctx context.Context) error
	Close            func()
}

var v = viper.New()

func init() {
	v.SetDefault(flagOutputPath, "")
	v.SetDefault(flagDebug, false)
	v.SetDefault(flagPort, "")
	v.SetDefault(flagPaths, "")
	v.SetDefault(flagWorkers, 0)
	v.SetDefault(flagYes, false)
	v.AutomaticEnv()
}

func ResolveConfig(cfg *config.Configuration) {
	if outputPath := v.GetString(flagOutputPath); outputPath != "" {
		cfg.AppDBPath = filepath.Join(outputPath, config.DefaultDBFileName)
		cfg.LogFile = filepath.Join(outputPath, config.DefaultLogFileName)
	}
	if workers := v.GetInt(flagWorkers); workers > 0 {
		cfg.Workers = workers
	}
	if v.GetBool(flagDebug) {
		cfg.LogLevel = "debug"
	}
	if port := v.GetString(flagPort); port != "" {
		cfg.ServerPort = port
	}
}

func (a *App) Execute() error {
	return a.newRootCmd().Execute()
}

func (a *App) newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "wandersort",
		Short:         "Find and organize duplicate media files",
		Long:          rootLongDesc,
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			v.BindPFlags(cmd.Flags())
			a.mergeCLIFlags()
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

	return rootCmd
}

func (a *App) mergeCLIFlags() {
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

func dbMissing(path string) bool {
	_, err := os.Stat(path)
	return err != nil
}

func outputDir(path string) string {
	return filepath.Dir(path)
}
