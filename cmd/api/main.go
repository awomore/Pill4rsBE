package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/awomore/Pill4rsBE/internal/ai"
	"github.com/awomore/Pill4rsBE/internal/config"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/handler"
	"github.com/awomore/Pill4rsBE/internal/integrations/meta"
	"github.com/awomore/Pill4rsBE/internal/middleware"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db connection: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("db ping: %v", err)
	}

	slog.Info("running migrations")
	m, err := migrate.New("file://migrations", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("migrate init: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		log.Fatalf("migrate up: %v", err)
	}
	slog.Info("migrations complete")

	queries := db.New(pool)
	authService := service.NewAuthService(queries, []byte(cfg.JWTSecret))
	authHandler := handler.NewAuthHandler(authService, cfg)
	workspaceService := service.NewWorkspaceService(queries)
	workspaceHandler := handler.NewWorkspaceHandler(workspaceService)
	aiClient := ai.NewClient(cfg.AnthropicAPIKey)
	metaClient := meta.NewClient(cfg.MetaAppID, cfg.MetaAppSecret, cfg.MetaRedirectURI)
	integrationsHandler := handler.NewIntegrationsHandler(queries, cfg, metaClient)
	syncService := service.NewSyncService(queries, metaClient, cfg.TokenEncryptionKey)
	syncHandler := handler.NewSyncHandler(queries, syncService)
	dashboardHandler := handler.NewDashboardHandler(queries)
	campaignService := service.NewCampaignService(queries, cfg.TokenEncryptionKey, metaClient)
	campaignHandler := handler.NewCampaignHandler(campaignService)
	actionService := service.NewActionService(queries, campaignService)
	actionHandler := handler.NewActionHandler(actionService)
	aiHandler := handler.NewAIHandler(queries, aiClient, actionService)

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = middleware.JSONErrorHandler
	e.Use(echomw.Recover())
	e.Use(middleware.Logger())
	e.Use(middleware.CORS(cfg.FrontendOrigin))
	e.Use(echomw.BodyLimit("1M"))
	e.Use(middleware.RateLimit(60))
	e.Use(middleware.Auth([]byte(cfg.JWTSecret)))

	e.GET("/health", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	auth := e.Group("/api/auth")
	auth.POST("/signup", authHandler.Signup)
	auth.POST("/login", authHandler.Login)
	auth.POST("/refresh", authHandler.Refresh)
	auth.POST("/logout", authHandler.Logout)
	auth.GET("/google", authHandler.GoogleLogin)
	auth.GET("/google/callback", authHandler.GoogleCallback)

	e.GET("/api/workspace", workspaceHandler.GetWorkspace)
	e.PATCH("/api/workspace", workspaceHandler.PatchWorkspace)
	e.POST("/api/ai/chat", aiHandler.Chat)

	e.GET("/api/integrations", integrationsHandler.List)
	e.GET("/api/integrations/meta/connect", integrationsHandler.MetaConnect)
	e.GET("/api/integrations/meta/callback", integrationsHandler.MetaCallback)
	e.GET("/api/integrations/meta/options", integrationsHandler.MetaOptions)
	e.PATCH("/api/integrations/meta", integrationsHandler.MetaConfigure)
	e.DELETE("/api/integrations/meta", integrationsHandler.MetaDisconnect)

	// POST /api/sync/trigger runs SyncWorkspace synchronously. A 6-hour
	// cron/worker would call service.SyncService.SyncWorkspace the same way.
	e.POST("/api/sync/trigger", syncHandler.Trigger)
	e.GET("/api/campaigns", syncHandler.ListCampaigns)

	e.GET("/api/dashboard/summary", dashboardHandler.Summary)

	// Unified multi-platform create + decoupled launch.
	e.POST("/api/campaigns", campaignHandler.Create)
	e.POST("/api/campaigns/:id/launch", campaignHandler.Launch)

	// Oma co-pilot: propose -> review -> approve/reject.
	e.POST("/api/ai/campaign/propose", aiHandler.ProposeCampaign)
	e.GET("/api/actions", actionHandler.List)
	e.POST("/api/actions/:id/approve", actionHandler.Approve)
	e.POST("/api/actions/:id/reject", actionHandler.Reject)

	addr := fmt.Sprintf(":%s", cfg.Port)
	go func() {
		slog.Info("starting server", "port", cfg.Port)
		if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	stop()
	slog.Info("shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
	}
	slog.Info("server stopped")
}
