// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package server is the HTTP surface for `wandersort serve`: one gin router,
// one route group, four handlers. It is an adapter over pkg/core and pkg/db —
// the operations themselves live there, so the CLI reaches them without going
// through here.
package server

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Router builds the full gin engine: middleware, the /workflow group, /ping,
// and the swagger UI.
func Router(log logger.Logger, wf *workflow.Workflow, database *db.DB) *gin.Engine {
	s := &server{log: log, wf: wf, db: database}

	router := gin.New()
	router.Use(logger.GinLogger(log))
	router.Use(recovery(log))
	router.Use(requestID())
	router.Use(cors())

	v1 := router.Group("/internal/v1")
	s.routes(v1.Group("/workflow"))

	router.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	v1.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))

	return router
}

/* ---------- errors ---------- */

type apiError struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e apiError) Error() string { return e.Message }

func badRequest(code, message string, details map[string]any) apiError {
	return apiError{Status: http.StatusBadRequest, Code: code, Message: message, Details: details}
}

func internalError(code, message string, details map[string]any) apiError {
	return apiError{Status: http.StatusInternalServerError, Code: code, Message: message, Details: details}
}

func notFound(code, message string, details map[string]any) apiError {
	return apiError{Status: http.StatusNotFound, Code: code, Message: message, Details: details}
}

/* ---------- response envelope ---------- */

type responseMeta struct {
	Timestamp string `json:"timestamp"`
	RequestID string `json:"request_id"`
}

type responseEnvelope struct {
	Success bool         `json:"success"`
	Data    any          `json:"data,omitempty"`
	Error   *apiError    `json:"error,omitempty"`
	Meta    responseMeta `json:"meta"`
}

func respondOK(c *gin.Context, status int, data any) {
	c.JSON(status, responseEnvelope{Success: true, Data: data, Meta: buildMeta(c)})
}

func respondError(c *gin.Context, err apiError) {
	c.JSON(err.Status, responseEnvelope{Success: false, Error: &err, Meta: buildMeta(c)})
}

func buildMeta(c *gin.Context) responseMeta {
	value, _ := c.Get("request_id")
	id, _ := value.(string)
	return responseMeta{Timestamp: time.Now().UTC().Format(time.RFC3339), RequestID: id}
}

/* ---------- middleware ---------- */

func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		c.Set("request_id", id)
		c.Writer.Header().Set("X-Request-ID", id)
		c.Next()
	}
}

func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func recovery(log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error(
					"panic recovered",
					"error", fmt.Sprintf("%v", r),
					"stack", string(debug.Stack()),
					"path", c.Request.URL.Path,
				)
				respondError(c, internalError("INTERNAL_ERROR", "unexpected server error", nil))
				c.Abort()
			}
		}()
		c.Next()
	}
}
