package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"

	"github.com/awomore/Pill4rsBE/internal/ai"
	"github.com/awomore/Pill4rsBE/internal/config"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/handler"
	"github.com/awomore/Pill4rsBE/internal/middleware"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

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
	aiHandler := handler.NewAIHandler(queries, aiClient)

	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.CORS(cfg.FrontendOrigin))
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

	addr := fmt.Sprintf(":%s", cfg.Port)
	slog.Info("starting server", "port", cfg.Port)
	e.Logger.Fatal(e.Start(addr))
}
