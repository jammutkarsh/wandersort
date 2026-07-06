// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package pipeline

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jammutkarsh/wandersort/internal/api"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

var _ api.Handlers = (*Handler)(nil)

type Handler struct {
	service *Service
	logger  logger.Logger
}

func NewHandler(log logger.Logger, service *Service) *Handler {
	return &Handler{service: service, logger: log}
}

func (h *Handler) SetupRoutes(v1 *gin.RouterGroup) {
	routerGroup := v1.Group("/pipeline")
	routerGroup.POST("/start", h.HandleStartScan)
	routerGroup.GET("/count", h.HandleGetFileCount)
}

// HandleStartScan godoc
// @Summary Start a new pipeline scan
// @Schemes http https
// @Description Submit root paths to the pipeline. The API validates directories, removes overlapping child paths, returns the effective scanPaths, and then starts scanning immediately
// @Tags Pipeline
// @Accept json
// @Produce json
// @Param request body StartScanRequest true "Start Scan Request"
// @Success 202 {object} StartScanResponse
// @Router /internal/v1/pipeline/start [post]
func (h *Handler) HandleStartScan(c *gin.Context) {
	var req StartScanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid request body", "error", err)
		api.RespondError(c, api.BadRequest("VALIDATION_ERROR", "Invalid request body", map[string]any{"error": err.Error()}))
		return
	}

	if len(req.RootPaths) == 0 {
		api.RespondError(c, api.BadRequest("VALIDATION_ERROR", "No root paths provided", map[string]any{"error": "At least one root path is required"}))
		return
	}

	var paths []string
	for _, p := range req.RootPaths {
		if path := strings.TrimSpace(p); path != "" {
			paths = append(paths, path)
		}
	}

	sessionID, scanPaths, err := h.service.StartScan(paths)
	if err != nil {
		h.logger.Error("Failed to start scan", "error", err)
		api.RespondError(c, api.InternalError("SCAN_START_FAILED", err.Error(), nil))
		return
	}

	api.RespondOK(c, http.StatusAccepted, StartScanResponse{
		SessionID: sessionID.String(),
		Status:    db.StatusStarted,
		Message:   "Scan started successfully",
		ScanPaths: scanPaths,
	})
}

// HandleGetFileCount godoc
// @Summary Get combined file counts
// @Schemes http https
// @Description Returns the number of files discovered by the scanner and the number hashed
// @Tags Pipeline
// @Produce json
// @Success 200 {object} FileCountResponse
// @Router /internal/v1/pipeline/count [get]
func (h *Handler) HandleGetFileCount(c *gin.Context) {
	resp, err := h.service.GetFileCount(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get file count", "error", err)
		api.RespondError(c, api.InternalError("FILE_COUNT_ERROR", "Failed to get file count", nil))
		return
	}

	api.RespondOK(c, http.StatusOK, resp)
}
