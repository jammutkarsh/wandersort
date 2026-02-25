package scans

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/internal/api"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func SetupRoutes(v1 *gin.RouterGroup, handler *Handler) {
	scans := v1.Group("/scans")
	scans.POST("/start", handler.HandleStartScan)
	scans.GET("/status", handler.HandleGetScanStatus)
	scans.GET("/count", handler.HandleGetFileCount)
	scans.POST("/cleanup-output", handler.HandleCleanupOutput)
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
// @Success 200 {object} StartScanResponse
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

	for i, path := range req.RootPaths {
		if path == "" {
			api.RespondError(c, api.BadRequest("VALIDATION_ERROR", "Empty root path provided", map[string]any{"error": "Root paths cannot be empty", "index": i}))
			return
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			api.RespondError(c, api.BadRequest("VALIDATION_ERROR", "Root path does not exist", map[string]any{"error": err.Error(), "index": i, "path": path}))
			return
		}
	}

	sessionID, err := h.service.StartScan(c.Request.Context(), req.RootPaths)
	if err != nil {
		h.logger.Error("Failed to start scan", "error", err)
		api.RespondError(c, api.InternalError("SCAN_START_FAILED", err.Error(), nil))
		return
	}

	resp := StartScanResponse{
		SessionID: sessionID.String(),
		Status:    "RUNNING",
		Message:   "Scan started successfully",
	}

	api.RespondOK(c, http.StatusOK, resp)
}

// HandleGetScanStatus godoc
// @Summary Get scan status
// @Schemes http https
// @Description Get the status of a scan session
// @Tags Scans
// @Accept json
// @Produce json
// @Param ScanStatusRequest query ScanStatusRequest true "Scan Status Request"
// @Success 200 {object} scanner.ScanSession
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

// HandleCleanupOutput godoc
// @Summary Clean up stale organized-library entries
// @Schemes http https
// @Description Checks every ORGANIZED entry in the file registry and removes those whose
// @Description files no longer exist on disk. Does NOT re-index or re-sort any files.
// @Tags Scans
// @Produce json
// @Success 200 {object} CleanupOutputResponse
// @Router /internal/v1/scans/cleanup-output [post]
func (h *Handler) HandleCleanupOutput(c *gin.Context) {
	resp, err := h.service.CleanupOrganizedFiles(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to cleanup organized files", "error", err)
		api.RespondError(c, api.InternalError("CLEANUP_FAILED", err.Error(), nil))
		return
	}

	api.RespondOK(c, http.StatusOK, resp)
}
