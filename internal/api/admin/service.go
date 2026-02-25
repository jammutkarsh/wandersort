// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"context"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

type Service struct {
	repo   *Repository
	logger logger.Logger
}

func NewService(log logger.Logger, repo *Repository) *Service {
	return &Service{repo: repo, logger: log}
}

func (s *Service) Reset(ctx context.Context) (ResetResponse, error) {
	s.logger.Warn("Admin reset triggered — deleting all application data")
	return s.repo.Reset(ctx)
}
