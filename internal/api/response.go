// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"time"

	"github.com/gin-gonic/gin"
)

type responseMeta struct {
	Timestamp string `json:"timestamp"`
	RequestID string `json:"request_id"`
}

type responseEnvelope struct {
	Success bool         `json:"success"`
	Data    any          `json:"data,omitempty"`
	Error   *APIError    `json:"error,omitempty"`
	Meta    responseMeta `json:"meta"`
}

func respondOK(c *gin.Context, status int, data any) {
	c.JSON(status, responseEnvelope{
		Success: true,
		Data:    data,
		Meta:    buildMeta(c),
	})
}

func RespondOK(c *gin.Context, status int, data any) {
	respondOK(c, status, data)
}

func respondError(c *gin.Context, apiErr APIError) {
	c.JSON(apiErr.Status, responseEnvelope{
		Success: false,
		Error:   &apiErr,
		Meta:    buildMeta(c),
	})
}

func RespondError(c *gin.Context, apiErr APIError) {
	respondError(c, apiErr)
}

func buildMeta(c *gin.Context) responseMeta {
	requestID, _ := c.Get("request_id")
	return responseMeta{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		RequestID: toString(requestID),
	}
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
