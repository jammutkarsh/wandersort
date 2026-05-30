package pipeline

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/core"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/status"
)

// Service orchestrates both scan submission and status streaming.
type Service struct {
	pipeline *core.Pipeline
	repo     *Repository
	logger   logger.Logger
	path     *path.Resolver
}

func NewService(log logger.Logger, pipeline *core.Pipeline, repo *Repository) *Service {
	return &Service{pipeline: pipeline, repo: repo, logger: log, path: path.New()}
}

func (s *Service) StartScan(paths []string) (uuid.UUID, error) {
	// Verify all paths before starting the scan to fail fast on invalid input and avoid partial scans.
	for _, p := range paths {
		isDir, err := s.path.IsDirectory(p)
		if err != nil {
			s.logger.Warn("Invalid root path", "path", p, "error", err)
			return uuid.Nil, err
		}

		if !isDir {
			s.logger.Warn("Root path is not a directory", "path", p)
			return uuid.Nil, fmt.Errorf("path is not a directory: %s", p)
		}
	}

	return s.pipeline.SubmitScan(paths)
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
