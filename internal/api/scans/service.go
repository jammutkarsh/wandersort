package scans

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/status"
)

// scanStarter is the capability the Service needs from the pipeline package.
type scanStarter interface {
	SubmitScan(ctx context.Context, rootPaths []string) (uuid.UUID, error)
	CleanupOrganizedFiles(ctx context.Context) (int64, error)
	StatusStream() chan status.PipelineStatus
	UnsubscribeStatus(ch chan status.PipelineStatus)
}

// scanRepository is the persistence capability the Service needs.
type scanRepository interface {
	GetScanStatus(ctx context.Context, sessionID uuid.UUID) (*ScanSession, error)
	GetFileCount(ctx context.Context) (int64, error)
}

type Service struct {
	scanner scanStarter
	repo    scanRepository
	logger  logger.Logger
}

// NewService wires together the Scanner, Repository and logger.
func NewService(log logger.Logger, sc scanStarter, repo scanRepository) *Service {
	return &Service{
		scanner: sc,
		repo:    repo,
		logger:  log,
	}
}

func (s *Service) StartScan(ctx context.Context, rootPaths []string) (uuid.UUID, error) {
	return s.scanner.SubmitScan(ctx, rootPaths)
}

func (s *Service) GetScanStatus(ctx context.Context, sessionID uuid.UUID) (*ScanSession, error) {
	return s.repo.GetScanStatus(ctx, sessionID)
}

func (s *Service) GetFileCount(ctx context.Context) (FileCountResponse, error) {
	count, err := s.repo.GetFileCount(ctx)
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

func (s *Service) SubscribeStatus() chan status.PipelineStatus {
	return s.scanner.StatusStream()
}

func (s *Service) UnsubscribeStatus(ch chan status.PipelineStatus) {
	s.scanner.UnsubscribeStatus(ch)
}
