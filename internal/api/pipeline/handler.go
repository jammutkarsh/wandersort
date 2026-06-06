package pipeline

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/jammutkarsh/wandersort/internal/api"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

var _ api.Handlers = (*Handler)(nil)

type Handler struct {
	service *Service
	logger  logger.Logger
}

func NewHandler(log logger.Logger, service *Service) *Handler {
	return &Handler{service: service, logger: log}
}

func (h *Handler) SetupRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/pipeline")
	g.POST("/start", h.HandleStartScan)
	g.GET("/ws", h.HandleWebSocket)
	g.GET("/count", h.HandleGetFileCount)
}

// HandleStartScan godoc
// @Summary Start a new pipeline scan
// @Schemes http https
// @Description Submit root paths to the pipeline. The API validates directories, removes overlapping child paths, returns the effective scanPaths, and then starts scanning immediately.
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
		Status:    "SCAN",
		Message:   "Scan started successfully",
		ScanPaths: scanPaths,
	})
}

// HandleWebSocket godoc
// @Summary Stream pipeline status via WebSocket
// @Schemes ws wss
// @Description Opens a WebSocket connection that pushes PipelineStatus JSON messages in real time.
// @Tags Pipeline
// @Success 101 {string} string "Switching Protocols"
// @Router /internal/v1/pipeline/ws [get]
func (h *Handler) HandleWebSocket(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Warn("WebSocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	ch := h.service.SubscribeStatus()
	defer h.service.UnsubscribeStatus(ch)

	// Pump status messages to the client until the connection closes or the
	// channel is drained.
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(msg); err != nil {
				h.logger.Warn("WebSocket write error", "error", err)
				return
			}
		}
	}
}

// HandleGetFileCount godoc
// @Summary Get combined file counts
// @Schemes http https
// @Description Returns the number of files discovered by the scanner and the number hashed.
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
