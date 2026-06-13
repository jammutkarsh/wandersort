package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jammutkarsh/wandersort/docs"
	"github.com/jammutkarsh/wandersort/internal/api"
	"github.com/jammutkarsh/wandersort/internal/api/admin"
	"github.com/jammutkarsh/wandersort/internal/api/pipeline"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/locationdb"
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

	// Initialize configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	// Logger
	logger := logger.New(cfg.LogLevel, cfg.LogConsole, cfg.LogFile)

	// Database (SQLite)
	sqliteDB, err := db.New(cfg.DatabasePath, logger)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}

	// locationDB(SQLite) (downloaded automatically if absent)
	lDB, err := locationdb.New(cfg.LocationDBPath, logger)
	if err != nil {
		log.Printf("warning: locationdb unavailable: %v", err)
		lDB = nil
	}

	// Ensure the DB get closed on any exit path — including unrecovered panics.
	// With locking_mode=EXCLUSIVE, a missing Close leaves the WAL/SHM files
	// locked and prevents the server from restarting.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic recovered during shutdown: %v", r)
		}
		logger.Info("Closing databases")
		if err := sqliteDB.Close(); err != nil {
			log.Printf("error closing database: %v", err)
		}
		if err := lDB.Close(); err != nil {
			log.Printf("error closing location database: %v", err)
		}
	}()

	// Create the unified workflow orchestrator
	workflow := workflow.NewWorkflow(ctx, sqliteDB, lDB, logger, cfg)

	// API handlers
	adminHandler := admin.NewHandler(logger, admin.NewService(logger, admin.NewRepository(sqliteDB)))
	pipelineHandler := pipeline.NewHandler(logger, pipeline.NewService(logger, workflow, pipeline.NewRepository(sqliteDB)))

	// Setup Gin router
	router := setupRouter(logger, cfg.Host, adminHandler, pipelineHandler)

	// HTTP server
	server := &http.Server{
		Addr:              ":" + cfg.ServerPort,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start server in a goroutine so it doesn't block the signal listener.
	go func() {
		logger.Info("Starting Server on", "port", cfg.ServerPort)
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("Shutting down", "signal", sig.String())

	// Cancel the root context to stop any background goroutines.
	// The explicit call ensures  the shutdown sequence happens
	// in the right order: cancel pipeline → wait for sessions → close DB → shutdown server.
	cancel()

	// Wait for pipeline workers to finish before closing the DB.
	workflow.Close()

	// Give in-flight requests up to 30 s to complete.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}

	logger.Info("Server stopped")
}

// setupRouter creates and configures the Gin router with all middleware and routes.
func setupRouter(l logger.Logger, host string, handlers ...api.Handlers) *gin.Engine {
	router := gin.New()

	// Global middleware
	router.Use(logger.GinLogger(l))
	router.Use(api.RecoveryMiddleware())
	router.Use(api.RequestIDMiddleware())
	router.Use(api.CORSMiddleware())

	v1 := router.Group("/internal/v1")
	for _, handler := range handlers {
		handler.SetupRoutes(v1)
	}

	router.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "pong"})
	})

	// Swagger docs
	docs.SwaggerInfo.Host = host
	v1.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))
	for _, v := range router.Routes() {
		l.Info("Registered Route", v.Method, v.Path)
	}

	return router
}
