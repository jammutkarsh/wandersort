// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/jammutkarsh/wandersort/docs"
	"github.com/jammutkarsh/wandersort/internal/api"
	"github.com/jammutkarsh/wandersort/internal/api/admin"
	"github.com/jammutkarsh/wandersort/internal/api/pipeline"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// @title           WanderSort API
// @version         1.0
// @description     API documentation for WanderSort
func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load failed: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	logger := logger.New(cfg.LogLevel, cfg.LogConsole, cfg.LogFile)

	// Verify exiftool is available before starting the server
	exiftoolPath, err := exiftool.Verify(ctx, logger, cfg.BinDir)
	if err != nil {
		logger.Error("exiftool verification failed", "error", err)
		os.Exit(1)
	}

	appDB, err := db.New(ctx, cfg.AppDBPath, db.AppDB, logger)
	if err != nil {
		logger.Error("appDB: failed to initialize", "error", err)
	}

	locationDB, err := db.New(ctx, cfg.LocationDBPath, db.LocationDB, logger)
	if err != nil {
		logger.Error("locationDB: failed to initialize", "error", err)
	}

	locationResolver, err := location.New(locationDB, cfg.LocationDBPath, logger)
	if err != nil {
		logger.Error("locationResolver: failed to initialize", "error", err)
	}

	// Ensure the DB get closed on any exit path — including unrecovered panics
	// With locking_mode=EXCLUSIVE, a missing Close leaves the WAL/SHM files
	// locked and prevents the server from restarting
	defer func() {
		if r := recover(); r != nil {
			logger.Error("panic recovered during shutdown", "error", fmt.Sprintf("%v", r))
		}

		logger.Info("Closing databases")

		if err := appDB.Close(); err != nil {
			logger.Error("error closing wandersort database", "error", err)
		}

		if err := locationDB.Close(); err != nil {
			logger.Error("error closing location database", "error", err)
		}
	}()

	// Create the unified workflow orchestrator
	workflow := workflow.NewWorkflow(ctx, appDB, locationResolver, logger, cfg, exiftoolPath)

	// API handlers
	adminHandler := admin.NewHandler(logger, admin.NewService(logger, admin.NewRepository(appDB)))
	pipelineHandler := pipeline.NewHandler(logger, pipeline.NewService(logger, workflow, pipeline.NewRepository(appDB)))

	// Setup Gin router
	router := setupRouter(logger, adminHandler, pipelineHandler)

	server := &http.Server{
		Addr:              ":" + cfg.ServerPort,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("Starting server", "port", cfg.ServerPort)
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server listen failed", "error", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	sig := <-quit

	logger.Info("Shutting down", "signal", sig.String())

	// Cancel the root context to stop any background goroutines
	// The explicit call ensures  the shutdown sequence happens
	// in the right order: cancel pipeline → wait for sessions → close DB → shutdown server
	cancel()
	// Wait for pipeline workers to finish before closing the DB
	workflow.Close()

	// Graceful HTTP shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("forced shutdown", "error", err)
	}

	logger.Info("Server stopped")
}

// setupRouter creates and configures the Gin router with all middleware and routes
func setupRouter(log logger.Logger, handlers ...api.Handlers) *gin.Engine {
	router := gin.New()

	// Global middleware
	router.Use(logger.GinLogger(log))
	router.Use(api.RecoveryMiddleware(log))
	router.Use(api.RequestIDMiddleware())
	router.Use(api.CORSMiddleware())

	v1 := router.Group("/internal/v1")

	for _, handler := range handlers {
		handler.SetupRoutes(v1)
	}

	router.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	v1.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))
	for _, v := range router.Routes() {
		log.Info("Registered Route", "method", v.Method, "path", v.Path)
	}

	return router
}
