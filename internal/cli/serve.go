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
	"github.com/jammutkarsh/wandersort/internal/api"
	"github.com/jammutkarsh/wandersort/internal/api/admin"
	"github.com/jammutkarsh/wandersort/internal/api/pipeline"
	vfsapi "github.com/jammutkarsh/wandersort/internal/api/vfs"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	_ "github.com/jammutkarsh/wandersort/swagger"
	"github.com/spf13/cobra"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// @title           WanderSort API
// @version         1.0
// @description     API documentation for WanderSort

func (a *App) newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP API server",
		Long: `Starts a long-running server that exposes the scan pipeline over a REST
API, with Swagger docs at /internal/v1/swagger. Runs until interrupted.`,
		Example: `wandersort serve
wandersort serve --port 8080`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runServe()
		},
	}

	cmd.Flags().StringP(flagPort, "p", "", "HTTP listen port (env: PORT)")
	return cmd
}

func (a *App) runServe() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Gin's verbose debug output only when the user asked for --debug.
	if a.Config.LogLevel == "debug" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	if err := a.InitAppDB(ctx); err != nil {
		return err
	}
	if err := a.EnsureDependencies(ctx); err != nil {
		return err
	}
	defer a.Close()

	l, err := lock.AcquireOutput(filepath.Dir(a.Config.LogFile))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer l.Unlock()

	wf := workflow.NewWorkflow(ctx, a.AppDB, a.LocationResolver, a.Log, a.Config, a.ExiftoolPath)

	adminHandler := admin.NewHandler(a.Log, a.AdminService())
	pipelineHandler := pipeline.NewHandler(a.Log, a.PipelineService(wf))
	vfsHandler := vfsapi.NewHandler(a.Log, a.AppDB)

	router := setupRouter(a.Log, adminHandler, pipelineHandler, vfsHandler)

	server := &http.Server{
		Addr:              ":" + a.Config.ServerPort,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		a.Log.Info(fmt.Sprintf("Server running on http://localhost:%s (press Ctrl-C to stop)", a.Config.ServerPort), logger.UserKey, true)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

	if err := server.Shutdown(shutdownCtx); err != nil {
		a.Log.Error("forced shutdown", "error", err)
	}

	a.Log.Info("Server stopped", logger.UserKey, true)
	return nil
}

func setupRouter(log logger.Logger, handlers ...api.Handlers) *gin.Engine {
	router := gin.New()

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

	return router
}
