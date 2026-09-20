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
	"github.com/awomore/Pill4rsBE/internal/integrations"
	"github.com/awomore/Pill4rsBE/internal/integrations/google"
	"github.com/awomore/Pill4rsBE/internal/integrations/meta"
	"github.com/awomore/Pill4rsBE/internal/integrations/tiktok"
	"github.com/awomore/Pill4rsBE/internal/integrations/zernio"
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
	walletService := service.NewWalletService(pool, queries)
	mediaStorage := service.NewLocalStorage(cfg.MediaDir, cfg.MediaPublicBaseURL)
	mediaService := service.NewMediaService(queries, mediaStorage)
	authService := service.NewAuthService(queries, []byte(cfg.JWTSecret))
	authHandler := handler.NewAuthHandler(authService, cfg)
	workspaceService := service.NewWorkspaceService(queries)
	workspaceHandler := handler.NewWorkspaceHandler(workspaceService)
	aiClient := ai.NewClient(cfg.AnthropicAPIKey)
	metaClient := meta.NewClient(cfg.MetaAppID, cfg.MetaAppSecret, cfg.MetaRedirectURI)
	tiktokClient := tiktok.NewClient(cfg.TikTokAppID, cfg.TikTokAppSecret, cfg.TikTokRedirectURI)
	googleClient := google.NewClient(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleAdsRedirectURI, cfg.GoogleAdsDeveloperToken, cfg.GoogleAdsLoginCustomerID)
	zernioClient := zernio.NewClient(cfg.ZernioAPIKey, cfg.ZernioBaseURL)
	zernioAdapters := zernio.NewAdapters(zernioClient)

	// Native adapters first, then Zernio covers every network we don't build
	// natively (LinkedIn, Pinterest, X, OpenAI, and Zernio's Meta/Google/TikTok).
	dataSources := []integrations.CampaignDataSource{metaClient, tiktokClient, googleClient}
	campaignAdapters := []integrations.CampaignAdapter{metaClient, tiktokClient, googleClient}
	for _, za := range zernioAdapters {
		dataSources = append(dataSources, za)
		campaignAdapters = append(campaignAdapters, za)
	}

	integrationsHandler := handler.NewIntegrationsHandler(queries, cfg, metaClient, tiktokClient, googleClient)
	zernioHandler := handler.NewZernioHandler(cfg, zernioClient, queries)
	syncService := service.NewSyncService(queries, cfg.TokenEncryptionKey, dataSources...)
	syncHandler := handler.NewSyncHandler(queries, syncService)
	dashboardHandler := handler.NewDashboardHandler(queries)
	campaignService := service.NewCampaignService(queries, cfg.TokenEncryptionKey, campaignAdapters...)
	actionService := service.NewActionService(queries, campaignService)
	spendGuard := service.NewSpendGuard(queries, walletService, campaignService)
	campaignService.SetSpendGuard(spendGuard)
	walletService.SetOnDebit(spendGuard.Enforce)
	syncService.SetBilling(walletService, spendGuard)
	campaignHandler := handler.NewCampaignHandler(campaignService, actionService)
	actionHandler := handler.NewActionHandler(actionService)
	aiHandler := handler.NewAIHandler(queries, aiClient, actionService, walletService)
	reviewHandler := handler.NewReviewHandler(campaignService, actionService)
	platformsHandler := handler.NewPlatformsHandler(queries)
	walletHandler := handler.NewWalletHandler(walletService, spendGuard)
	mediaHandler := handler.NewMediaHandler(mediaService)
	flutterwaveHandler := handler.NewFlutterwaveHandler(cfg, walletService, spendGuard, queries)

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = middleware.JSONErrorHandler
	e.Use(echomw.Recover())
	e.Use(middleware.Logger())
	e.Use(middleware.CORS(cfg.FrontendOrigin))
	e.Use(echomw.BodyLimit("66M"))
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
	e.GET("/api/integrations/tiktok/connect", integrationsHandler.TikTokConnect)
	e.GET("/api/integrations/tiktok/callback", integrationsHandler.TikTokCallback)
	e.GET("/api/integrations/google/connect", integrationsHandler.GoogleConnect)
	e.GET("/api/integrations/google/callback", integrationsHandler.GoogleCallback)
	// Zernio — unified ads provider for the networks we don't build natively.
	e.GET("/api/integrations/zernio/connect", zernioHandler.Connect)
	e.GET("/api/integrations/zernio/callback", zernioHandler.Callback)
	e.GET("/api/integrations/zernio/callback/:profile_id", zernioHandler.Callback)
	e.POST("/api/integrations/zernio/sync", zernioHandler.Sync)
	// Per-account management (a business may have many accounts per platform).
	e.GET("/api/integrations/accounts/:id/options", integrationsHandler.AccountOptions)
	e.PATCH("/api/integrations/accounts/:id", integrationsHandler.AccountConfigure)
	e.PATCH("/api/integrations/accounts/:id/billing", integrationsHandler.SetAccountBilling)
	e.DELETE("/api/integrations/accounts/:id", integrationsHandler.Disconnect)

	// POST /api/sync/trigger runs SyncWorkspace synchronously. A 6-hour
	// cron/worker would call service.SyncService.SyncWorkspace the same way.
	e.POST("/api/sync/trigger", syncHandler.Trigger)
	e.GET("/api/campaigns", syncHandler.ListCampaigns)

	e.GET("/api/dashboard/summary", dashboardHandler.Summary)

	// Unified multi-platform create + decoupled launch.
	e.POST("/api/campaigns", campaignHandler.Create)
	e.POST("/api/campaigns/:id/launch", campaignHandler.Launch)

	// Operate: pause/resume/budget + Oma health card with one-click apply.
	e.POST("/api/campaigns/:id/pause", campaignHandler.Pause)
	e.POST("/api/campaigns/:id/resume", campaignHandler.Resume)
	e.PATCH("/api/campaigns/:id", campaignHandler.UpdateBudget)
	e.GET("/api/campaigns/:id/health", campaignHandler.Health)
	e.POST("/api/campaigns/:id/health/apply", campaignHandler.ApplyHealth)

	// Oma co-pilot: propose -> review -> approve/reject.
	e.POST("/api/ai/campaign/propose", aiHandler.ProposeCampaign)
	e.POST("/api/ai/review", reviewHandler.Run)
	e.GET("/api/actions", actionHandler.List)
	e.POST("/api/actions/:id/approve", actionHandler.Approve)
	e.POST("/api/actions/:id/reject", actionHandler.Reject)

	// Platform capabilities matrix — static, fetched once.
	e.GET("/api/platforms/capabilities", platformsHandler.Capabilities)
	// Platform overview — capability matrix + this workspace's connection state.
	e.GET("/api/platforms", platformsHandler.Overview)

	// Forecast — estimates reach/spend for an unsaved targeting spec.
	e.POST("/api/campaigns/forecast", campaignHandler.Forecast)

	// Prepaid wallet + ledger, and creative media uploads.
	e.GET("/api/wallet", walletHandler.Get)
	e.GET("/api/wallet/transactions", walletHandler.Transactions)
	e.POST("/api/wallet/topup", walletHandler.TopUp)
	e.POST("/api/wallet/checkout", flutterwaveHandler.Checkout)
	e.POST("/api/webhooks/flutterwave", flutterwaveHandler.Webhook)
	e.GET("/api/media", mediaHandler.List)
	e.POST("/api/media", mediaHandler.Upload)
	e.DELETE("/api/media/:id", mediaHandler.Delete)

	// Uploaded media is served publicly (like a CDN) so ad platforms can fetch
	// it by URL. The auth middleware exempts /media.
	e.Static("/media", cfg.MediaDir)

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
