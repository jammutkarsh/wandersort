package hash

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/internal/api"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func SetupRoutes(v1 *gin.RouterGroup, handler *Handler) {
	g := v1.Group("/hash")
	g.GET("/progress", handler.HandleGetProgress)
	g.GET("/stats", handler.HandleGetStats)
}

type Handler struct {
	service *Service
	logger  logger.Logger
}

func NewHandler(log logger.Logger, service *Service) *Handler {
	return &Handler{service: service, logger: log}
}

// HandleGetProgress godoc
// @Summary Get hashing progress for a scan session
// @Description Returns files_discovered, files_hashed, files_errored and percent_complete for the given session.
// @Tags Hash
// @Produce json
// @Param session_id query string true "Scan session UUID"
// @Success 200 {object} HashProgressResponse
// @Router /internal/v1/hash/progress [get]
func (h *Handler) HandleGetProgress(c *gin.Context) {
	var req HashProgressRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		h.logger.Warn("Invalid query parameters", "error", err)
		api.RespondError(c, api.BadRequest("VALIDATION_ERROR", "Invalid query parameters", map[string]any{"error": err.Error()}))
		return
	}

	sessionID, err := uuid.Parse(req.SessionID)
	if err != nil {
		api.RespondError(c, api.BadRequest("VALIDATION_ERROR", "Invalid session_id format", map[string]any{"error": err.Error()}))
		return
	}

	resp, err := h.service.GetProgress(c.Request.Context(), sessionID)
	if err != nil {
		h.logger.Warn("Failed to get hash progress", "session_id", sessionID, "error", err)
		api.RespondError(c, api.NotFound("SESSION_NOT_FOUND", "Session not found", map[string]any{"session_id": req.SessionID}))
		return
	}

	api.RespondOK(c, http.StatusOK, resp)
}

// HandleGetStats godoc
// @Summary Get overall content group statistics
// @Description Returns aggregate counts across all content groups: total groups, duplicates, masters elected.
// @Tags Hash
// @Produce json
// @Success 200 {object} HashStatsResponse
// @Router /internal/v1/hash/stats [get]
func (h *Handler) HandleGetStats(c *gin.Context) {
	resp, err := h.service.GetStats(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get hash stats", "error", err)
		api.RespondError(c, api.InternalError("STATS_ERROR", err.Error(), nil))
		return
	}

	api.RespondOK(c, http.StatusOK, resp)
}
