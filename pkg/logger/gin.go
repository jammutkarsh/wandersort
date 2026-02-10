package logger

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// GinLogger returns a gin middleware that logs requests via the provided
// `Logger` but skips logging for health and swagger asset routes and any
// requests that result in a 404 response.
func GinLogger(l Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		if path == "/ping" || strings.HasPrefix(path, "/internal/v1/swagger/") || strings.HasPrefix(path, "/internal/v1/swagger/") {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		if status == http.StatusNotFound {
			return
		}

		latency := time.Since(start)
		l.Info("http_request",
			"status", status,
			"method", c.Request.Method,
			"path", path,
			"latency", latency.String(),
		)
	}
}
