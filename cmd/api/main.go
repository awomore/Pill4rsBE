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
	"github.com/awomore/Pill4rsBE/internal/integrations/dv360"
	"github.com/awomore/Pill4rsBE/internal/integrations/google"
	"github.com/awomore/Pill4rsBE/internal/integrations/meta"
	"github.com/awomore/Pill4rsBE/internal/integrations/tiktok"
	"github.com/awomore/Pill4rsBE/internal/integrations/zernio"
	"github.com/awomore/Pill4rsBE/internal/middleware"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
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
	apiKeyService := service.NewAPIKeyService(queries)
	idempotencyStore := service.NewIdempotencyStore(queries)
	agencyService := service.NewAgencyService(queries)
	walletService := service.NewWalletService(pool, queries)
	adminService := service.NewAdminService(queries, walletService)
	userService := service.NewUserService(queries, adminService)
	supportService := service.NewSupportService(pool, queries)
	mediaStorage := service.NewLocalStorage(cfg.MediaDir, cfg.MediaPublicBaseURL)
	mediaService := service.NewMediaService(queries, mediaStorage)
	authService := service.NewAuthService(queries, []byte(cfg.JWTSecret))
	authService.SetPlatformAdminEmails(cfg.PlatformAdminEmails)
	adminService.Bootstrap(ctx, cfg.PlatformAdminEmails)
	authHandler := handler.NewAuthHandler(authService, cfg)
	workspaceService := service.NewWorkspaceService(queries)
	workspaceHandler := handler.NewWorkspaceHandler(workspaceService)
	aiClient := ai.NewClient(cfg.AnthropicAPIKey)
	metaClient := meta.NewClient(cfg.MetaAppID, cfg.MetaAppSecret, cfg.MetaRedirectURI)
	managedMetaService := service.NewManagedMetaService(queries, metaClient, cfg, walletService)
	tiktokClient := tiktok.NewClient(cfg.TikTokAppID, cfg.TikTokAppSecret, cfg.TikTokRedirectURI)
	managedTikTokService := service.NewManagedTikTokService(queries, tiktokClient, cfg, walletService)
	googleClient := google.NewClient(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleAdsRedirectURI, cfg.GoogleAdsDeveloperToken, cfg.GoogleAdsLoginCustomerID)
	managedGoogleService := service.NewManagedGoogleService(queries, googleClient, cfg, walletService)
	dv360Client := dv360.NewClient(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.ProgrammaticRedirectURI)
	zernioClient := zernio.NewClient(cfg.ZernioAPIKey, cfg.ZernioBaseURL)
	zernioAdapters := zernio.NewAdapters(zernioClient)

	// Native adapters first, then Zernio covers every network we don't build
	// natively (LinkedIn, Pinterest, X, OpenAI, and Zernio's Meta/Google/TikTok).
	// The programmatic DSP is the buying backbone for display/video.
	dataSources := []integrations.CampaignDataSource{metaClient, tiktokClient, googleClient, dv360Client}
	campaignAdapters := []integrations.CampaignAdapter{metaClient, tiktokClient, googleClient, dv360Client}
	for _, za := range zernioAdapters {
		dataSources = append(dataSources, za)
		campaignAdapters = append(campaignAdapters, za)
	}

	integrationsHandler := handler.NewIntegrationsHandler(queries, cfg, metaClient, tiktokClient, googleClient, managedMetaService, managedTikTokService, managedGoogleService)
	dv360Handler := handler.NewDV360Handler(cfg, dv360Client, queries)
	zernioHandler := handler.NewZernioHandler(cfg, zernioClient, queries)
	syncService := service.NewSyncService(queries, cfg.TokenEncryptionKey, dataSources...)
	syncHandler := handler.NewSyncHandler(queries, syncService)
	dashboardHandler := handler.NewDashboardHandler(queries)
	campaignService := service.NewCampaignService(queries, cfg.TokenEncryptionKey, campaignAdapters...)
	actionService := service.NewActionService(queries, campaignService)
	spendGuard := service.NewSpendGuard(queries, walletService, campaignService)
	campaignService.SetSpendGuard(spendGuard)
	walletService.SetOnDebit(spendGuard.Enforce)
	walletService.SetOnCredit(func(ctx context.Context, ws uuid.UUID) {
		managedMetaService.SyncSpendCaps(ctx, ws)
		managedTikTokService.SyncBudgets(ctx, ws)
		managedGoogleService.SyncSpendCaps(ctx, ws)
	})
	syncService.SetBilling(walletService, spendGuard)
	campaignHandler := handler.NewCampaignHandler(campaignService, actionService)
	actionHandler := handler.NewActionHandler(actionService)
	aiHandler := handler.NewAIHandler(queries, aiClient, actionService, walletService)
	reviewHandler := handler.NewReviewHandler(campaignService, actionService)
	platformsHandler := handler.NewPlatformsHandler(queries)
	walletHandler := handler.NewWalletHandler(walletService, spendGuard)
	mediaHandler := handler.NewMediaHandler(mediaService)
	apiKeyHandler := handler.NewAPIKeyHandler(apiKeyService, queries)
	agencyHandler := handler.NewAgencyHandler(agencyService, cfg)
	adminHandler := handler.NewAdminHandler(adminService, cfg)
	userHandler := handler.NewUserHandler(userService)
	supportHandler := handler.NewSupportHandler(supportService)
	flutterwaveHandler := handler.NewFlutterwaveHandler(cfg, walletService, spendGuard, queries)

	// Idempotency records are replayed for 24h, then pruned hourly.
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = idempotencyStore.DeleteExpired(ctx, time.Now().Add(-24*time.Hour))
			}
		}
	}()

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = middleware.JSONErrorHandler
	e.Use(echomw.Recover())
	e.Use(middleware.Logger())
	e.Use(middleware.CORS(cfg.FrontendOrigin))
	e.Use(echomw.BodyLimit("66M"))
	e.Use(middleware.RateLimit(60))
	e.Use(middleware.Auth([]byte(cfg.JWTSecret), apiKeyService))

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

	// Signed-in user profile, sessions, and accessible workspaces.
	e.GET("/api/user", userHandler.Profile)
	e.POST("/api/user/password", userHandler.ChangePassword)
	e.GET("/api/user/sessions", userHandler.Sessions)
	e.DELETE("/api/user/sessions/:id", userHandler.RevokeSession)
	e.GET("/api/user/workspaces", userHandler.Workspaces)

	e.GET("/api/integrations", integrationsHandler.List)
	e.GET("/api/integrations/meta/connect", integrationsHandler.MetaConnect)
	e.GET("/api/integrations/meta/callback", integrationsHandler.MetaCallback)
	e.POST("/api/integrations/meta/managed-account", integrationsHandler.MetaManagedAccount)
	e.GET("/api/integrations/tiktok/connect", integrationsHandler.TikTokConnect)
	e.GET("/api/integrations/tiktok/callback", integrationsHandler.TikTokCallback)
	e.POST("/api/integrations/tiktok/managed-account", integrationsHandler.TikTokManagedAccount)
	e.GET("/api/integrations/google/connect", integrationsHandler.GoogleConnect)
	e.GET("/api/integrations/google/callback", integrationsHandler.GoogleCallback)
	e.POST("/api/integrations/google/managed-account", integrationsHandler.GoogleManagedAccount)
	// DV360 — DSP buying backbone (campaign -> insertion order -> line item).
	e.GET("/api/integrations/programmatic/connect", dv360Handler.Connect)
	e.GET("/api/integrations/programmatic/start", dv360Handler.Start)
	e.GET("/api/integrations/programmatic/callback", dv360Handler.Callback)
	e.POST("/api/integrations/programmatic/sync", dv360Handler.Sync)
	e.POST("/api/integrations/programmatic/managed-advertiser", dv360Handler.ManagedAdvertiser)
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
	e.GET("/api/wallet/holds", walletHandler.Holds)
	e.POST("/api/wallet/topup", walletHandler.TopUp)
	e.POST("/api/wallet/checkout", flutterwaveHandler.Checkout)
	e.POST("/api/webhooks/flutterwave", flutterwaveHandler.Webhook)
	e.GET("/api/media", mediaHandler.List)
	e.POST("/api/media", mediaHandler.Upload)
	e.DELETE("/api/media/:id", mediaHandler.Delete)

	// Pill4rs platform-operator API (global control, support, audit).
	admin := e.Group("/api/admin", middleware.RequirePlatformAdmin(adminService))
	admin.GET("/overview", adminHandler.Overview)
	admin.GET("/agencies", adminHandler.Agencies)
	admin.GET("/agencies/:id/clients", adminHandler.AgencyClients)
	admin.GET("/users", adminHandler.Users)
	admin.GET("/admins", adminHandler.Admins)
	admin.POST("/admins", adminHandler.GrantAdmin)
	admin.DELETE("/admins/:user_id", adminHandler.RevokeAdmin)
	admin.POST("/workspaces/:id/switch", adminHandler.SwitchWorkspace)
	admin.POST("/workspaces/:id/topup", adminHandler.TopUp)
	admin.GET("/audit-logs", adminHandler.AuditLogs)
	admin.GET("/support/tickets", supportHandler.AdminList)
	admin.GET("/support/tickets/:id", supportHandler.AdminGet)
	admin.POST("/support/tickets/:id/messages", supportHandler.AdminReply)
	admin.PATCH("/support/tickets/:id", supportHandler.AdminUpdate)
	admin.GET("/support/summary", supportHandler.AdminSummary)

	// Client support tickets.
	e.POST("/api/support/tickets", supportHandler.Create)
	e.GET("/api/support/tickets", supportHandler.List)
	e.GET("/api/support/tickets/:id", supportHandler.Get)
	e.POST("/api/support/tickets/:id/messages", supportHandler.Reply)

	// Agency -> client workspace hierarchy and team management (UI sessions).
	e.GET("/api/agency", agencyHandler.Get)
	e.GET("/api/agency/summary", agencyHandler.Summary)
	e.GET("/api/agency/clients", agencyHandler.Clients)
	e.POST("/api/agency/clients", agencyHandler.CreateClient)
	e.POST("/api/agency/clients/:id/switch", agencyHandler.SwitchClient)
	e.GET("/api/agency/members", agencyHandler.Members)
	e.POST("/api/agency/members", agencyHandler.AddMember)
	e.PATCH("/api/agency/members/:user_id", agencyHandler.UpdateMember)
	e.DELETE("/api/agency/members/:user_id", agencyHandler.RemoveMember)

	// Workspace API keys for external marketing brands. Key management itself
	// requires a UI session or a wildcard key.
	e.GET("/api/api-keys", apiKeyHandler.List, middleware.RequireScope("*"))
	e.POST("/api/api-keys", apiKeyHandler.Create, middleware.RequireScope("*"))
	e.DELETE("/api/api-keys/:id", apiKeyHandler.Revoke, middleware.RequireScope("*"))

	// Stable v1 surface for automated campaign management. Authenticate with
	// `Authorization: Bearer p4_live_...` or `X-API-Key: p4_live_...`.
	v1 := e.Group("/api/v1")
	idem := middleware.Idempotency(idempotencyStore)
	v1.GET("/accounts", integrationsHandler.List, middleware.RequireScope("accounts:read"))
	v1.POST("/google/managed-accounts", integrationsHandler.GoogleManagedAccount, middleware.RequireScope("accounts:write"), idem)
	v1.POST("/meta/managed-accounts", integrationsHandler.MetaManagedAccount, middleware.RequireScope("accounts:write"), idem)
	v1.POST("/tiktok/managed-accounts", integrationsHandler.TikTokManagedAccount, middleware.RequireScope("accounts:write"), idem)
	v1.GET("/campaigns", syncHandler.ListCampaigns, middleware.RequireScope("campaigns:read"))
	v1.POST("/campaigns", campaignHandler.Create, middleware.RequireScope("campaigns:write"), idem)
	v1.POST("/campaigns/forecast", campaignHandler.Forecast, middleware.RequireScope("campaigns:read"))
	v1.POST("/campaigns/:id/launch", campaignHandler.Launch, middleware.RequireScope("campaigns:write"), idem)
	v1.POST("/campaigns/:id/pause", campaignHandler.Pause, middleware.RequireScope("campaigns:write"), idem)
	v1.POST("/campaigns/:id/resume", campaignHandler.Resume, middleware.RequireScope("campaigns:write"), idem)
	v1.PATCH("/campaigns/:id", campaignHandler.UpdateBudget, middleware.RequireScope("campaigns:write"), idem)
	v1.GET("/campaigns/:id/health", campaignHandler.Health, middleware.RequireScope("campaigns:read"))
	v1.POST("/sync", syncHandler.Trigger, middleware.RequireScope("campaigns:write"), idem)
	v1.GET("/reports/summary", dashboardHandler.Summary, middleware.RequireScope("reports:read"))

	// DV360 audience + custom bidding management.
	v1.POST("/programmatic/managed-advertisers", dv360Handler.ManagedAdvertiser, middleware.RequireScope("accounts:write"), idem)
	v1.GET("/programmatic/audiences", dv360Handler.Audiences, middleware.RequireScope("audiences:read"))
	v1.POST("/programmatic/lookalike-audiences", dv360Handler.CreateLookalike, middleware.RequireScope("audiences:write"), idem)
	v1.GET("/programmatic/custom-bidding-algorithms", dv360Handler.CustomBiddingAlgorithms, middleware.RequireScope("bidding:read"))
	v1.POST("/programmatic/custom-bidding-algorithms", dv360Handler.CreateCustomBiddingAlgorithm, middleware.RequireScope("bidding:write"), idem)
	v1.GET("/programmatic/custom-bidding-algorithms/:id/scripts", dv360Handler.CustomBiddingScripts, middleware.RequireScope("bidding:read"))
	v1.POST("/programmatic/custom-bidding-algorithms/:id/scripts", dv360Handler.UploadCustomBiddingScript, middleware.RequireScope("bidding:write"), idem)
	v1.GET("/programmatic/custom-bidding-algorithms/:id/rules", dv360Handler.CustomBiddingRules, middleware.RequireScope("bidding:read"))
	v1.POST("/programmatic/custom-bidding-algorithms/:id/rules", dv360Handler.UploadCustomBiddingRules, middleware.RequireScope("bidding:write"), idem)

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
