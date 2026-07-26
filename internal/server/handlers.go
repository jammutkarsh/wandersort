// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	corevfs "github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// server holds everything the four handlers need. There is no service or
// repository tier: the operations live in pkg/core/workflow and pkg/db, which
// the CLI calls directly too.
type server struct {
	log logger.Logger
	wf  *workflow.Workflow
	db  *db.DB
}

func (s *server) routes(g *gin.RouterGroup) {
	g.POST("/scan", s.handleScan)
	g.POST("/reset", s.handleReset)
	g.GET("/:id/tree", s.handleTree)
	g.POST("/:id/confirm", s.handleConfirm)
}

/* ---------- request/response bodies ---------- */

// ScanRequest is the body for POST /workflow/scan
type ScanRequest struct {
	RootPaths []string `json:"rootPaths" binding:"required"`
}

// ScanResponse is returned after a scan is successfully submitted
type ScanResponse struct {
	SessionID string   `json:"sessionId"`
	Status    string   `json:"status"`
	Message   string   `json:"message"`
	ScanPaths []string `json:"scanPaths"`
}

// TreeResponse is the proposed directory tree for a session (folder names only).
type TreeResponse struct {
	SessionID string         `json:"sessionId"`
	Tree      []corevfs.Node `json:"tree"`
}

// ConfirmRequest carries the (possibly edited) tree back for reconcile.
type ConfirmRequest struct {
	Tree []corevfs.Node `json:"tree" binding:"required"`
}

// ConfirmResponse acknowledges an applied review.
type ConfirmResponse struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

/* ---------- handlers ---------- */

// handleScan godoc
// @Summary Start a new scan
// @Schemes http https
// @Description Submit root paths to the pipeline. The API validates directories, removes overlapping child paths, returns the effective scanPaths, and then starts scanning immediately
// @Tags Workflow
// @Accept json
// @Produce json
// @Param request body ScanRequest true "Scan Request"
// @Success 202 {object} ScanResponse
// @Router /internal/v1/workflow/scan [post]
func (s *server) handleScan(c *gin.Context) {
	var req ScanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.log.Warn("Invalid request body", "error", err)
		respondError(c, badRequest("VALIDATION_ERROR", "Invalid request body", map[string]any{"error": err.Error()}))
		return
	}

	var paths []string
	for _, p := range req.RootPaths {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	if len(paths) == 0 {
		respondError(c, badRequest("VALIDATION_ERROR", "No root paths provided", map[string]any{"error": "At least one root path is required"}))
		return
	}

	sessionID, scanPaths, err := s.wf.SubmitScan(paths)
	if err != nil {
		s.log.Error("Failed to start scan", "error", err)
		respondError(c, internalError("SCAN_START_FAILED", err.Error(), nil))
		return
	}

	respondOK(c, http.StatusAccepted, ScanResponse{
		SessionID: sessionID.String(),
		Status:    db.StatusStarted,
		Message:   "Scan started successfully",
		ScanPaths: scanPaths,
	})
}

// handleReset godoc
// @Summary Reset all application data
// @Description Deletes all scan sessions, file registry entries, and file metadata in a single transaction. Irreversible
// @Tags Workflow
// @Produce json
// @Success 200 {object} db.ResetCounts
// @Router /internal/v1/workflow/reset [post]
func (s *server) handleReset(c *gin.Context) {
	s.log.Warn("Admin reset triggered — deleting all application data")
	counts, err := s.db.ResetAll(c.Request.Context())
	if err != nil {
		s.log.Error("Admin reset failed", "error", err)
		respondError(c, internalError("RESET_FAILED", err.Error(), nil))
		return
	}

	s.log.Warn("Admin reset completed",
		"scanSessions", counts.ScanSessionsDeleted,
		"files", counts.FilesDeleted,
		"fileMetadata", counts.FileMetadataDeleted)

	respondOK(c, http.StatusOK, counts)
}

// handleTree godoc
// @Summary Get the proposed VFS directory tree
// @Schemes http https
// @Description Returns the proposed destination hierarchy (folder names only) for a session's files, for review before the move
// @Tags Workflow
// @Produce json
// @Param id path string true "Session ID"
// @Success 200 {object} TreeResponse
// @Router /internal/v1/workflow/{id}/tree [get]
func (s *server) handleTree(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondError(c, badRequest("VALIDATION_ERROR", "Invalid session id", map[string]any{"error": err.Error()}))
		return
	}

	tree, err := corevfs.BuildTree(c.Request.Context(), id, s.db)
	if err != nil {
		s.log.Error("Failed to build vfs tree", "sessionId", id.String(), "error", err)
		respondError(c, internalError("VFS_TREE_ERROR", "Failed to build vfs tree", nil))
		return
	}
	if len(tree) == 0 {
		respondError(c, notFound("NOT_FOUND", "No VFS proposal for this session", nil))
		return
	}

	respondOK(c, http.StatusOK, TreeResponse{SessionID: id.String(), Tree: tree})
}

// handleConfirm godoc
// @Summary Confirm (approve/correct) the VFS directory tree
// @Schemes http https
// @Description Accepts the possibly edited tree, applies renames to target paths, marks entries APPROVED, and remembers renamed locations for future scans
// @Tags Workflow
// @Accept json
// @Produce json
// @Param id path string true "Session ID"
// @Param request body ConfirmRequest true "Edited tree"
// @Success 200 {object} ConfirmResponse
// @Router /internal/v1/workflow/{id}/confirm [post]
func (s *server) handleConfirm(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondError(c, badRequest("VALIDATION_ERROR", "Invalid session id", map[string]any{"error": err.Error()}))
		return
	}

	var req ConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, badRequest("VALIDATION_ERROR", "Invalid request body", map[string]any{"error": err.Error()}))
		return
	}

	if err := corevfs.Confirm(c.Request.Context(), id, s.db, req.Tree); err != nil {
		if errors.Is(err, corevfs.ErrInvalidTree) {
			respondError(c, badRequest("VALIDATION_ERROR", err.Error(), nil))
			return
		}
		if errors.Is(err, corevfs.ErrNoProposal) {
			respondError(c, notFound("NOT_FOUND", "No VFS proposal for this session", nil))
			return
		}
		s.log.Error("Failed to confirm vfs tree", "sessionId", id.String(), "error", err)
		respondError(c, internalError("VFS_CONFIRM_ERROR", "Failed to confirm vfs tree", nil))
		return
	}

	respondOK(c, http.StatusOK, ConfirmResponse{
		SessionID: id.String(),
		Status:    db.StatusApproved,
		Message:   "VFS proposal approved",
	})
}
