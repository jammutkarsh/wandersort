package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jammutkarsh/wandersort/internal/api"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func SetupRoutes(v1 *gin.RouterGroup, handler *Handler) {
	adminGroup := v1.Group("/admin")
	adminGroup.POST("/reset", handler.HandleReset)
}

type Handler struct {
	service *Service
	logger  logger.Logger
}

func NewHandler(log logger.Logger, service *Service) *Handler {
	return &Handler{service: service, logger: log}
}

// HandleReset godoc
// @Summary Reset all application data
// @Description Deletes all scan sessions, file registry entries, content groups and group members in a single transaction. Irreversible.
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
		"scan_sessions", resp.ScanSessionsDeleted,
		"files", resp.FilesDeleted,
		"content_groups", resp.ContentGroupsDeleted,
		"group_members", resp.GroupMembersDeleted)

	api.RespondOK(c, http.StatusOK, resp)
}
