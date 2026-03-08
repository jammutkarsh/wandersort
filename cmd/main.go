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
	"github.com/jammutkarsh/wandersort/internal/api/hash"
	"github.com/jammutkarsh/wandersort/internal/api/scans"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core"
	"github.com/jammutkarsh/wandersort/pkg/db"
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

	// Ensure the DB is closed on ANY exit path — including unrecovered panics.
	// With locking_mode=EXCLUSIVE, a missing Close leaves the WAL/SHM files
	// locked and prevents the server from restarting.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic recovered during shutdown: %v", r)
		}
		logger.Info("Closing database")
		if err := sqliteDB.Close(); err != nil {
			log.Printf("error closing database: %v", err)
		}
	}()

	// Create the unified pipeline orchestrator
	pipeline := core.NewPipeline(ctx, sqliteDB, logger, cfg)

	// API handlers
	adminHandler := admin.NewHandler(logger, admin.NewService(logger, admin.NewRepository(sqliteDB)))
	scansHandler := scans.NewHandler(logger, scans.NewService(logger, pipeline, scans.NewRepository(sqliteDB)))
	hashHandler := hash.NewHandler(logger, hash.NewService(logger, hash.NewRepository(sqliteDB)))

	// Setup Gin router
	router := setupRouter(logger, adminHandler, scansHandler, hashHandler, cfg.Host)

	// HTTP server
	srv := &http.Server{
		Addr:              ":" + cfg.ServerPort,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start server in a goroutine so it doesn't block the signal listener.
	go func() {
		logger.Info("Starting Server on", "port", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("Shutting down", "signal", sig.String())

	// Cancel the root context to stop any background goroutines.
	cancel()

	// Wait for pipeline workers to finish before closing the DB.
	pipeline.Close()

	// Give in-flight requests up to 30 s to complete.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}

	logger.Info("Server stopped")
}

// setupRouter creates and configures the Gin router with all middleware and routes.
func setupRouter(l logger.Logger, aH *admin.Handler, sH *scans.Handler, hH *hash.Handler, host string) *gin.Engine {
	router := gin.New()

	router.Use(logger.GinLogger(l))
	router.Use(api.RecoveryMiddleware())
	router.Use(api.RequestIDMiddleware())
	router.Use(api.CORSMiddleware())

	// API routes
	const basePath = "/internal/v1"
	v1 := router.Group(basePath)
	admin.SetupRoutes(v1, aH)
	scans.SetupRoutes(v1, sH)
	hash.SetupRoutes(v1, hH)

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
