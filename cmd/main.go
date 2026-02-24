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
	"github.com/jammutkarsh/wandersort/internal/api/scans"
	"github.com/jammutkarsh/wandersort/pkg/config"
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

	// ── OpenTelemetry ──────────────────────────────────────────────────────
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

	// ── Logger ─────────────────────────────────────────────────────────────
	// OTel bridge must be enabled AFTER telemetry.Setup() registers the
	// global LoggerProvider.
	wsLogger := logger.New(cfg.LogLevel, cfg.LogConsole, cfg.LogFile, cfg.OTelEnabled)

	// Initialize database
	psql := db.InitDB(ctx, cfg.Postgres, wsLogger)
	defer psql.Close()

	// Setup API dependencies
	scansHandler := scans.NewHandler(scans.NewService(ctx, psql, wsLogger, cfg.OutputPath), wsLogger)

	// Setup Gin router
	router := gin.New()
	if cfg.OTelEnabled {
		router.Use(otelgin.Middleware("wandersort"))
	}
	router.Use(logger.GinLogger(wsLogger))
	router.Use(api.RecoveryMiddleware())
	router.Use(api.RequestIDMiddleware())
	router.Use(api.CORSMiddleware())

	// Setup API routes
	basePath := "/internal/v1"
	v1 := router.Group(basePath)
	scans.SetupRoutes(v1, scansHandler)

	router.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "pong"})
	})
	// Docs
	docs.SwaggerInfo.Host = os.Getenv("HOST")
	v1.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))

	// ── HTTP server ────────────────────────────────────────────────────────
	port := cfg.ServerPort
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine so it doesn't block the signal listener.
	go func() {
		wsLogger.Info("Server starting", "port", port, "otel", cfg.OTelEnabled)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// ── Graceful shutdown ──────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	wsLogger.Info("Shutting down", "signal", sig.String())

	// Cancel the root context to stop any background goroutines.
	cancel()

	// Give in-flight requests up to 30 s to complete.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}

	wsLogger.Info("Server stopped")
}
