package scans

import (
	"context"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/status"
)

// scanStarter is the capability the Service needs from the pipeline package.
type scanStarter interface {
	SubmitScan(rootPaths []string) (uuid.UUID, error)
	StatusStream() chan status.PipelineStatus
	UnsubscribeStatus(ch chan status.PipelineStatus)
}

type Service struct {
	scanner scanStarter
	repo    *Repository
	logger  logger.Logger
}

// NewService wires together the Scanner, Repository and logger.
func NewService(log logger.Logger, sc scanStarter, repo *Repository) *Service {
	return &Service{
		scanner: sc,
		repo:    repo,
		logger:  log,
	}
}

func (s *Service) StartScan(rootPaths []string) (uuid.UUID, error) {
	return s.scanner.SubmitScan(rootPaths)
}

func (s *Service) GetScanStatus(ctx context.Context, sessionID uuid.UUID) (*ScanSession, error) {
	return s.repo.GetScanStatus(ctx, sessionID)
}

func (s *Service) SubscribeStatus() chan status.PipelineStatus {
	return s.scanner.StatusStream()
}

func (s *Service) UnsubscribeStatus(ch chan status.PipelineStatus) {
	s.scanner.UnsubscribeStatus(ch)
}

func (s *Service) GetFileCount(ctx context.Context) (FileCountResponse, error) {
	count, err := s.repo.GetFileCount(ctx)
	if err != nil {
		return FileCountResponse{}, err
	}
	return FileCountResponse{TotalFiles: count}, nil
}
