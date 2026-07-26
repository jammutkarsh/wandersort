// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Router registers a static child (/workflow/scan) next to a param child
// (/workflow/:id/tree). gin's radix tree panics on that combination in older
// versions, and the panic would only show up at `serve` startup — so build the
// real router and drive one request through the envelope.
func TestRouterRegistersRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := Router(logger.NewNoopLogger(), nil, nil)

	want := map[string]bool{
		"POST /internal/v1/workflow/scan":        true,
		"POST /internal/v1/workflow/reset":       true,
		"GET /internal/v1/workflow/:id/tree":     true,
		"POST /internal/v1/workflow/:id/confirm": true,
	}
	for _, r := range router.Routes() {
		delete(want, r.Method+" "+r.Path)
	}
	if len(want) != 0 {
		t.Errorf("routes not registered: %v", want)
	}

	// a bad session id never reaches the nil db, so this exercises the
	// error envelope end to end
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/v1/workflow/not-a-uuid/tree", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header missing — middleware not wired")
	}
}
