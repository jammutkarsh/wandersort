package scans

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

type Service struct {
	scanner *scanner.Scanner
	logger  logger.Logger
}

func NewService(ctx context.Context, db *pgxpool.Pool, log logger.Logger, outputPath string) *Service {
	return &Service{
		scanner: scanner.NewScanner(db, log, outputPath),
		logger:  log,
	}
}

func (s *Service) StartScan(ctx context.Context, rootPaths []string) (uuid.UUID, error) {
	return s.scanner.StartScan(ctx, rootPaths)
}

func (s *Service) GetScanStatus(ctx context.Context, sessionID uuid.UUID) (*scanner.ScanSession, error) {
	return s.scanner.GetScanStatus(ctx, sessionID)
}

func (s *Service) GetFileCount(ctx context.Context) (FileCountResponse, error) {
	count, err := s.scanner.GetFileCount(ctx)
	if err != nil {
		return FileCountResponse{}, err
	}
	return FileCountResponse{TotalFiles: count}, nil
}

// CleanupOrganizedFiles removes registry entries for ORGANIZED files that no longer
// exist on disk. This does NOT re-index or re-sort — it is a deletion-only pass.
func (s *Service) CleanupOrganizedFiles(ctx context.Context) (CleanupOutputResponse, error) {
	deleted, err := s.scanner.CleanupOrganizedFiles(ctx)
	if err != nil {
		return CleanupOutputResponse{}, err
	}
	return CleanupOutputResponse{
		DeletedCount: deleted,
		Message:      fmt.Sprintf("Removed %d stale entries from the organized library registry", deleted),
	}, nil
}
