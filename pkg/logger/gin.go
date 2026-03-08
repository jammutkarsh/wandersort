package logger

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var ignoredPaths []string = []string{
	"/ping",
	"/internal/v1/swagger/",
}

func GinLogger(l Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		for _, ignore := range ignoredPaths {
			if strings.HasPrefix(path, ignore) {
				c.Next()
				return
			}
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
