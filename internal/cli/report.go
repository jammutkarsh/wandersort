package cli

import (
	"context"
	"fmt"

	"github.com/jammutkarsh/wandersort/internal/api/pipeline"
	"github.com/spf13/cobra"
)

func (a *App) newReportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "report",
		Short: "Show a summary of the last scan",
		Long:  `Prints a report of scanned files, hashed files, and detected duplicates.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReport()
		},
	}
}

func (a *App) runReport() error {
	if dbMissing(a.Config.AppDBPath) {
		return fmt.Errorf("no database found — run 'wandersort scan' first")
	}

	ctx := context.Background()

	result, err := pipeline.RunReport(ctx, a.Config.AppDBPath)
	if err != nil {
		return fmt.Errorf("report: %w", err)
	}

	pipeline.PrintReport(result)
	return nil
}
