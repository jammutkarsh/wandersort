package hash

import (
	"context"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

type Service struct {
	repo   *Repository
	logger logger.Logger
}

func NewService(log logger.Logger, repo *Repository) *Service {
	return &Service{repo: repo, logger: log}
}

func (s *Service) GetProgress(ctx context.Context, sessionID uuid.UUID) (*HashProgressResponse, error) {
	return s.repo.GetProgress(ctx, sessionID)
}

func (s *Service) GetStats(ctx context.Context) (*HashStatsResponse, error) {
	return s.repo.GetStats(ctx)
}
