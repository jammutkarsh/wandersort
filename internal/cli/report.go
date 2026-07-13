package cli

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

type Report struct {
	FilesScanned    int64 `json:"filesScanned"`
	FilesHashed     int64 `json:"filesHashed"`
	FilesDuplicated int64 `json:"filesDuplicated"`
}

var (
	labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	valueStyle = lipgloss.NewStyle().Bold(true)
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).MarginBottom(1)
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
	if _, err := os.Stat(a.Config.AppDBPath); os.IsNotExist(err) {
		return fmt.Errorf("no database found — nothing to report on")
	}

	ctx := context.Background()

	result, err := a.generateReport(ctx, a.Config.AppDBPath)
	if err != nil {
		return fmt.Errorf("report: %w", err)
	}

	a.printReport(result)
	return nil
}

func (a *App) generateReport(ctx context.Context, dbPath string) (*Report, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_journal=OFF", dbPath)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db read-only: %w", err)
	}
	defer sqlDB.Close()

	var result Report

	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_registry`).Scan(&result.FilesScanned); err != nil {
		return nil, fmt.Errorf("count file_registry: %w", err)
	}

	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_metadata`).Scan(&result.FilesHashed); err != nil {
		return nil, fmt.Errorf("count file_metadata: %w", err)
	}

	if err := sqlDB.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM file_metadata WHERE file_hash IN (SELECT file_hash FROM file_metadata GROUP BY file_hash HAVING COUNT(*) > 1)`,
	).Scan(&result.FilesDuplicated); err != nil {
		return nil, fmt.Errorf("count duplicates: %w", err)
	}

	return &result, nil
}

func (a *App) printReport(r *Report) {
	fmt.Println(titleStyle.Render("WanderSort Report"))
	fmt.Println(labelStyle.Render("Files scanned:    ") + valueStyle.Render(fmt.Sprintf("%d", r.FilesScanned)))
	fmt.Println(labelStyle.Render("Files hashed:     ") + valueStyle.Render(fmt.Sprintf("%d", r.FilesHashed)))
	fmt.Println(labelStyle.Render("Duplicates found: ") + valueStyle.Render(fmt.Sprintf("%d", r.FilesDuplicated)))
}
