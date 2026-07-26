// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jammutkarsh/wandersort/internal/server"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	swaggerdocs "github.com/jammutkarsh/wandersort/swagger"
	"github.com/spf13/cobra"
)

// @title           WanderSort API
// @version         1.0
// @description     API documentation for WanderSort

func (a *app) newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP API server",
		Long: `Starts a long-running server that exposes the scan pipeline over a REST
API, with Swagger docs at /internal/v1/swagger. Runs until interrupted.`,
		Example: `wandersort serve
wandersort serve --port 8080`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// same pipeline as scan, same requirement
			if err := requireConfigured(); err != nil {
				return err
			}
			return a.runServe()
		},
	}

	cmd.Flags().StringP(flagPort, "p", "", "HTTP listen port (env: PORT)")
	return cmd
}

func (a *app) runServe() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gin.SetMode(gin.ReleaseMode)

	if err := a.initAppDB(ctx); err != nil {
		return err
	}
	if err := a.ensureDependencies(ctx, nil); err != nil {
		return err
	}
	defer a.closeDBs()

	// same as scan: API-driven scans run the same VFS phase, so this library's
	// DB needs the globally-saved anchors before anything proposes folders
	if err := a.syncAnchors(ctx); err != nil {
		return fmt.Errorf("anchors: %w", err)
	}

	l, err := lock.AcquireOutput(filepath.Dir(a.Config.LogFile))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer l.Unlock()

	wf := workflow.NewWorkflow(ctx, a.AppDB, a.Log, a.Config, workflow.ReadyDeps(a.ExiftoolPath, a.LocationResolver))

	// swag leaves Host/Schemes blank at generation time (the port is only known
	// at runtime) — an empty Host makes swagger-ui's "Try it out" build a
	// schemeless request URL, which browsers reject as a CORS failure.
	swaggerdocs.SwaggerInfo.Host = "localhost:" + a.Config.ServerPort
	swaggerdocs.SwaggerInfo.Schemes = []string{"http"}

	router := server.Router(a.Log, wf, a.AppDB)

	httpServer := &http.Server{
		Addr:              ":" + a.Config.ServerPort,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		a.Log.Info(fmt.Sprintf("Server running on http://localhost:%s (press Ctrl-C to stop)", a.Config.ServerPort), logger.UserKey, true)
		a.Log.Info(fmt.Sprintf("Swagger docs at http://localhost:%s/internal/v1/swagger/index.html", a.Config.ServerPort), logger.UserKey, true)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.Log.Error("server listen failed", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	a.Log.Info("Shutting down…", logger.UserKey, true, "signal", sig.String())

	cancel()
	wf.Close()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		a.Log.Error("forced shutdown", "error", err)
	}

	a.Log.Info("Server stopped", logger.UserKey, true)
	return nil
}
