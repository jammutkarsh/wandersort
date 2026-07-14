// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jammutkarsh/wandersort/internal/api"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

type Handler struct {
	service *Service
	logger  logger.Logger
}

var _ api.Handlers = (*Handler)(nil)

func NewHandler(log logger.Logger, service *Service) *Handler {
	return &Handler{service: service, logger: log}
}

func (h *Handler) SetupRoutes(v1 *gin.RouterGroup) {
	adminGroup := v1.Group("/admin")
	adminGroup.POST("/reset", h.HandleReset)
}

// HandleReset godoc
// @Summary Reset all application data
// @Description Deletes all scan sessions, file registry entries, and file metadata in a single transaction. Irreversible
// @Tags Admin
// @Produce json
// @Success 200 {object} ResetResponse
// @Router /internal/v1/admin/reset [post]
func (h *Handler) HandleReset(c *gin.Context) {
	resp, err := h.service.Reset(c.Request.Context())
	if err != nil {
		h.logger.Error("Admin reset failed", "error", err)
		api.RespondError(c, api.InternalError("RESET_FAILED", err.Error(), nil))
		return
	}

	h.logger.Warn("Admin reset completed",
		"scanSessions", resp.ScanSessionsDeleted,
		"files", resp.FilesDeleted,
		"fileMetadata", resp.FileMetadataDeleted)

	api.RespondOK(c, http.StatusOK, resp)
}
