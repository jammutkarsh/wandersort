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
	"github.com/jammutkarsh/wandersort/pkg/core/hasher"
	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/queue"
	"github.com/jammutkarsh/wandersort/pkg/telemetry"
	"github.com/riverqueue/river"
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
	wsLogger := logger.New(cfg.LogLevel, cfg.LogConsole, cfg.LogFile, cfg.OTelEnabled)

	// Database
	psql, err := db.InitDB(ctx, cfg.Postgres, wsLogger)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer psql.Close()

	// Core services — hasher is independent; scanner needs the enqueuer
	// which we get from the queue, so we create it after queue setup.
	h := hasher.NewHasher(psql, wsLogger)

	// Register River workers and start the queue.
	workers := river.NewWorkers()
	scanWorker := &scanner.ScanTaskWorker{} // Scanner assigned after construction
	river.AddWorker(workers, scanWorker)
	river.AddWorker(workers, &hasher.HashTaskWorker{Hasher: h})

	riverClient, enqueuer, err := queue.New(ctx, psql, queue.Config{
		MaxConcurrentScans:   cfg.MaxConcurrentScans,
		MaxConcurrentHashers: cfg.MaxConcurrentHashers,
	}, workers)
	if err != nil {
		log.Fatalf("failed to create river client: %v", err)
	}

	// Now that we have the enqueuer, build the scanner and wire it into the worker.
	sc := scanner.NewScanner(psql, wsLogger, cfg.OutputPath, enqueuer)
	scanWorker.Scanner = sc

	// API handlers
	adminHandler := admin.NewHandler(wsLogger, admin.NewService(wsLogger, admin.NewRepository(psql)))
	scansHandler := scans.NewHandler(wsLogger, scans.NewService(wsLogger, sc, scans.NewRepository(psql)))
	hashHandler := hash.NewHandler(wsLogger, hash.NewService(wsLogger, hash.NewRepository(psql)))

	// Setup Gin router
	router := setupRouter(cfg, wsLogger, adminHandler, scansHandler, hashHandler)

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
		wsLogger.Info("Server starting", "port", port, "otel", cfg.OTelEnabled)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	wsLogger.Info("Shutting down", "signal", sig.String())

	// Cancel the root context to stop any background goroutines.
	cancel()

	// Give in-flight requests up to 30 s to complete.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Stop River first so in-flight jobs complete before we close the DB pool.
	if err := riverClient.Stop(shutdownCtx); err != nil {
		log.Printf("warn: river stop error: %v", err)
	}

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}

	wsLogger.Info("Server stopped")
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
