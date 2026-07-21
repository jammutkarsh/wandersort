// Package vfs is the HTTP surface for issue #8's Virtual FS review: read the
// proposed directory tree, submit corrections. It is a thin adapter over the
// shared reconcile core in pkg/core/vfs.
package vfs

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/jammutkarsh/wandersort/internal/api"
	corevfs "github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

var _ api.Handlers = (*Handler)(nil)

// Handler is a thin adapter over the shared reconcile core in pkg/core/vfs —
// no repository or service tier, the core funcs are the data layer.
type Handler struct {
	db     *db.DB
	logger logger.Logger
}

func NewHandler(log logger.Logger, database *db.DB) *Handler {
	return &Handler{db: database, logger: log}
}

func (h *Handler) SetupRoutes(v1 *gin.RouterGroup) {
	sessions := v1.Group("/sessions")
	sessions.GET("/:id/vfs", h.HandleGetTree)
	sessions.POST("/:id/vfs/confirm", h.HandleConfirm)
}

// HandleGetTree godoc
// @Summary Get the proposed VFS directory tree
// @Schemes http https
// @Description Returns the proposed destination hierarchy (folder names only) for a session's files, for review before the move
// @Tags VFS
// @Produce json
// @Param id path string true "Session ID"
// @Success 200 {object} TreeResponse
// @Router /internal/v1/sessions/{id}/vfs [get]
func (h *Handler) HandleGetTree(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.RespondError(c, api.BadRequest("VALIDATION_ERROR", "Invalid session id", map[string]any{"error": err.Error()}))
		return
	}

	tree, err := corevfs.BuildTree(c.Request.Context(), id, h.db)
	if err != nil {
		h.logger.Error("Failed to build vfs tree", "sessionId", id.String(), "error", err)
		api.RespondError(c, api.InternalError("VFS_TREE_ERROR", "Failed to build vfs tree", nil))
		return
	}
	if len(tree) == 0 {
		api.RespondError(c, api.NotFound("NOT_FOUND", "No VFS proposal for this session", nil))
		return
	}

	api.RespondOK(c, http.StatusOK, TreeResponse{SessionID: id.String(), Tree: tree})
}

// HandleConfirm godoc
// @Summary Confirm (approve/correct) the VFS directory tree
// @Schemes http https
// @Description Accepts the possibly edited tree, applies renames to target paths, marks entries APPROVED, and remembers renamed locations for future scans
// @Tags VFS
// @Accept json
// @Produce json
// @Param id path string true "Session ID"
// @Param request body ConfirmRequest true "Edited tree"
// @Success 200 {object} ConfirmResponse
// @Router /internal/v1/sessions/{id}/vfs/confirm [post]
func (h *Handler) HandleConfirm(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.RespondError(c, api.BadRequest("VALIDATION_ERROR", "Invalid session id", map[string]any{"error": err.Error()}))
		return
	}

	var req ConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, api.BadRequest("VALIDATION_ERROR", "Invalid request body", map[string]any{"error": err.Error()}))
		return
	}

	if err := corevfs.Confirm(c.Request.Context(), id, h.db, req.Tree); err != nil {
		if errors.Is(err, corevfs.ErrInvalidTree) {
			api.RespondError(c, api.BadRequest("VALIDATION_ERROR", err.Error(), nil))
			return
		}
		if errors.Is(err, corevfs.ErrNoProposal) {
			api.RespondError(c, api.NotFound("NOT_FOUND", "No VFS proposal for this session", nil))
			return
		}
		h.logger.Error("Failed to confirm vfs tree", "sessionId", id.String(), "error", err)
		api.RespondError(c, api.InternalError("VFS_CONFIRM_ERROR", "Failed to confirm vfs tree", nil))
		return
	}

	api.RespondOK(c, http.StatusOK, ConfirmResponse{
		SessionID: id.String(),
		Status:    db.StatusApproved,
		Message:   "VFS proposal approved",
	})
}
