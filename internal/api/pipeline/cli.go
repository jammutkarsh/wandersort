package pipeline

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
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

func RunScan(ctx context.Context, log logger.Logger, appDB *db.DB, locationResolver *location.Resolver, cfg *config.Configuration, exiftoolPath string, rootPaths []string) error {
	wf := workflow.NewWorkflow(ctx, appDB, locationResolver, log, cfg, exiftoolPath)

	repo := NewRepository(appDB)
	svc := NewService(log, wf, repo)

	sessionID, scanPaths, err := svc.StartScan(rootPaths)
	if err != nil {
		return fmt.Errorf("start scan: %w", err)
	}

	log.Info("Scan started", "sessionId", sessionID, "scanPaths", scanPaths)

	wf.Close()
	return nil
}

func RunReport(ctx context.Context, dbPath string) (*Report, error) {
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

	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM file_metadata WHERE file_hash IN (SELECT file_hash FROM file_metadata GROUP BY file_hash HAVING COUNT(*) > 1)`,
	).Scan(&result.FilesDuplicated); err != nil {
		return nil, fmt.Errorf("count duplicates: %w", err)
	}

	return &result, nil
}

func PrintReport(r *Report) {
	fmt.Println(titleStyle.Render("WanderSort Report"))
	fmt.Println(labelStyle.Render("Files scanned:    ") + valueStyle.Render(fmt.Sprintf("%d", r.FilesScanned)))
	fmt.Println(labelStyle.Render("Files hashed:     ") + valueStyle.Render(fmt.Sprintf("%d", r.FilesHashed)))
	fmt.Println(labelStyle.Render("Duplicates found: ") + valueStyle.Render(fmt.Sprintf("%d", r.FilesDuplicated)))
}
