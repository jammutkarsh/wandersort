package main

import (
	"context"
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/jammutkarsh/wandersort/docs"
	"github.com/jammutkarsh/wandersort/internal/api"
	"github.com/jammutkarsh/wandersort/internal/api/scans"
	"github.com/jammutkarsh/wandersort/pkg/config"
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
	cfg := config.Load()

	// Setup structured logging: pretty console + JSON appended to log.json
	wsLogger := logger.New(
		logger.WithBackend(logger.BackendBoth),
		logger.WithLevel("info"),
	)

	// Initialize database
	psql := db.InitDB(ctx, cfg.Postgres, wsLogger)
	defer psql.Close()

	// Setup API dependencies
	scansHandler := scans.NewHandler(scans.NewService(ctx, psql, wsLogger), wsLogger)

	// Setup Gin router
	router := gin.New()
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

	// Start server
	port := cfg.ServerPort
	if port == "" {
		port = "8080"
	}

	wsLogger.Info("Server starting", "port", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
