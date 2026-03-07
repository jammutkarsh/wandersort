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
	"github.com/jammutkarsh/wandersort/pkg/telemetry"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
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

	// OTEL
	if cfg.OTelEnabled {
		shutdown, err := telemetry.Setup(ctx)
		if err != nil {
			log.Printf("warn: failed to initialise OpenTelemetry: %v", err)
		} else {
			defer func() {
				if err := shutdown(ctx); err != nil {
					log.Printf("warn: OTel shutdown error: %v", err)
				}
			}()
		}
	}

	// Logger
	logger := logger.New(cfg.LogLevel, cfg.OTelEnabled, cfg.LogConsole, cfg.LogFile)

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
	p := core.NewPipeline(ctx, sqliteDB, logger, cfg)

	// API handlers
	adminHandler := admin.NewHandler(logger, admin.NewService(logger, admin.NewRepository(sqliteDB)))
	scansHandler := scans.NewHandler(logger, scans.NewService(logger, p, scans.NewRepository(sqliteDB)))
	hashHandler := hash.NewHandler(logger, hash.NewService(logger, hash.NewRepository(sqliteDB)))

	// Setup Gin router
	router := setupRouter(cfg, logger, adminHandler, scansHandler, hashHandler)

	// HTTP server
	port := cfg.ServerPort
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start server in a goroutine so it doesn't block the signal listener.
	go func() {
		logger.Info("Server starting", "port", port, "otel", cfg.OTelEnabled)
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
	p.Close()

	// Give in-flight requests up to 30 s to complete.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}

	logger.Info("Server stopped")
}

// setupRouter creates and configures the Gin router with all middleware and routes.
func setupRouter(
	cfg *config.Configuration,
	wsLogger logger.Logger,
	adminHandler *admin.Handler,
	scansHandler *scans.Handler,
	hashHandler *hash.Handler,
) *gin.Engine {
	router := gin.New()

	// Middleware stack
	if cfg.OTelEnabled {
		router.Use(otelgin.Middleware("wandersort"))
	}
	router.Use(logger.GinLogger(wsLogger))
	router.Use(api.RecoveryMiddleware())
	router.Use(api.RequestIDMiddleware())
	router.Use(api.CORSMiddleware())

	// API routes
	const basePath = "/internal/v1"
	v1 := router.Group(basePath)
	admin.SetupRoutes(v1, adminHandler)
	scans.SetupRoutes(v1, scansHandler)
	hash.SetupRoutes(v1, hashHandler)

	router.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "pong"})
	})

	// Swagger docs
	docs.SwaggerInfo.Host = os.Getenv("HOST")
	v1.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))

	return router
}
