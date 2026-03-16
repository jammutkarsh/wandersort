package pipeline

import (
	"context"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/core"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/status"
)

// Service orchestrates both scan submission and status streaming.
type Service struct {
	pipeline *core.Pipeline
	repo     *Repository
	logger   logger.Logger
}

func NewService(log logger.Logger, pipeline *core.Pipeline, repo *Repository) *Service {
	return &Service{pipeline: pipeline, repo: repo, logger: log}
}

func (s *Service) StartScan(rootPaths []string) (uuid.UUID, error) {
	return s.pipeline.SubmitScan(rootPaths)
}

func (s *Service) SubscribeStatus() chan status.PipelineStatus {
	return s.pipeline.StatusStream()
}

func (s *Service) UnsubscribeStatus(ch chan status.PipelineStatus) {
	s.pipeline.UnsubscribeStatus(ch)
}

func (s *Service) GetFileCount(ctx context.Context) (FileCountResponse, error) {
	return s.repo.GetFileCount(ctx)
}
