// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package hash

import (
	"context"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// hashRepository is the persistence capability the Service needs.
type hashRepository interface {
	GetProgress(ctx context.Context, sessionID uuid.UUID) (*HashProgressResponse, error)
	GetStats(ctx context.Context) (*HashStatsResponse, error)
}

type Service struct {
	repo   hashRepository
	logger logger.Logger
}

func NewService(log logger.Logger, repo hashRepository) *Service {
	return &Service{repo: repo, logger: log}
}

func (s *Service) GetProgress(ctx context.Context, sessionID uuid.UUID) (*HashProgressResponse, error) {
	return s.repo.GetProgress(ctx, sessionID)
}

func (s *Service) GetStats(ctx context.Context) (*HashStatsResponse, error) {
	return s.repo.GetStats(ctx)
}
