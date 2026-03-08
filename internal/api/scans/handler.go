package scans

import (
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/internal/api"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func SetupRoutes(v1 *gin.RouterGroup, handler *Handler) {
	scans := v1.Group("/scans")
	scans.POST("/start", handler.HandleStartScan)
	scans.GET("/status", handler.HandleGetScanStatus)
	scans.GET("/stream", handler.HandleStreamStatus)
	scans.GET("/count", handler.HandleGetFileCount)
}

type Handler struct {
	service *Service
	logger  logger.Logger
}

func NewHandler(log logger.Logger, service *Service) *Handler {
	return &Handler{service: service, logger: log}
}

// HandleStartScan godoc
// @Summary Start a new scan session
// @Schemes http https
// @Description Start a new scan session with specified root paths
// @Tags Scans
// @Accept json
// @Produce json
// @Param request body StartScanRequest true "Start Scan Request"
// @Success 202 {object} StartScanResponse
// @Router /internal/v1/scans/start [post]
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

	var paths = []string{}
	for _, p := range req.RootPaths {
		path := strings.TrimSpace(p)
		if path != "" {
			paths = append(paths, path)
		}
	}

	sessionID, err := h.service.StartScan(paths)
	if err != nil {
		h.logger.Error("Failed to start scan", "error", err)
		api.RespondError(c, api.InternalError("SCAN_START_FAILED", err.Error(), nil))
		return
	}

	resp := StartScanResponse{
		SessionID: sessionID.String(),
		Status:    "SCAN",
		Message:   "Scan started successfully",
	}

	api.RespondOK(c, http.StatusAccepted, resp)
}

// HandleGetScanStatus godoc
// @Summary Get scan status
// @Schemes http https
// @Description Get the status of a scan session
// @Tags Scans
// @Accept json
// @Produce json
// @Param ScanStatusRequest query ScanStatusRequest true "Scan Status Request"
// @Success 200 {object} ScanSession
// @Router /internal/v1/scans/status [get]
func (h *Handler) HandleGetScanStatus(c *gin.Context) {
	var req ScanStatusRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		h.logger.Warn("Invalid query parameters", "error", err)
		api.RespondError(c, api.BadRequest("VALIDATION_ERROR", "Invalid query parameters", map[string]any{"error": err.Error()}))
		return
	}

	sessionID, err := uuid.Parse(req.SessionID)
	if err != nil {
		api.RespondError(c, api.BadRequest("VALIDATION_ERROR", "Invalid session ID format", map[string]any{"error": err.Error()}))
		return
	}

	session, err := h.service.GetScanStatus(c.Request.Context(), sessionID)
	if err != nil {
		h.logger.Warn("Failed to get scan status", "session_id", sessionID, "error", err)
		api.RespondError(c, api.NotFound("SESSION_NOT_FOUND", "Session not found", map[string]any{"session_id": req.SessionID}))
		return
	}

	api.RespondOK(c, http.StatusOK, session)
}

// HandleStreamStatus godoc
// @Summary Stream scan status via SSE
// @Schemes http https
// @Description Stream the unified scan status using Server-Sent Events (SSE). Keep-alive.
// @Tags Scans
// @Produce text/event-stream
// @Success 200 {string} string "SSE Event Stream"
// @Router /internal/v1/scans/stream [get]
func (h *Handler) HandleStreamStatus(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")

	ch := h.service.SubscribeStatus()
	defer h.service.UnsubscribeStatus(ch)

	c.Stream(func(w io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			return false
		case msg, ok := <-ch:
			if !ok {
				return false
			}
			c.SSEvent("message", msg)
			return true
		}
	})
}

// HandleGetFileCount godoc
// @Summary Get total file count
// @Schemes http https
// @Description Get the total number of files scanned across all sessions
// @Tags Scans
// @Accept json
// @Produce json
// @Success 200 {object} FileCountResponse "Total file count"
// @Router /internal/v1/scans/count [get]
func (h *Handler) HandleGetFileCount(c *gin.Context) {
	resp, err := h.service.GetFileCount(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get file count", "error", err)
		api.RespondError(c, api.InternalError("FILE_COUNT_ERROR", "Failed to get file count", nil))
		return
	}

	api.RespondOK(c, http.StatusOK, resp)
}
