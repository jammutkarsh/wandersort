package admin

import (
	"context"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// resetter is the persistence capability the Service needs.
type resetter interface {
	Reset(ctx context.Context) (ResetResponse, error)
}

type Service struct {
	repo   resetter
	logger logger.Logger
}

func NewService(log logger.Logger, repo resetter) *Service {
	return &Service{repo: repo, logger: log}
}

func (s *Service) Reset(ctx context.Context) (ResetResponse, error) {
	s.logger.Warn("Admin reset triggered — deleting all application data")
	return s.repo.Reset(ctx)
}
