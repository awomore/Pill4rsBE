package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/awomore/Pill4rsBE/internal/config"
	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/integrations"
	"github.com/awomore/Pill4rsBE/internal/integrations/dv360"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

const dv360StateCookie = "programmatic_oauth_state"

// DV360Handler connects a workspace's Display & Video 360 advertisers and keeps
// them synced as Pill4rs ad_accounts. DV360 is the DSP buying backbone: every
// campaign created for these accounts is pushed as campaign -> IO -> line item.
type DV360Handler struct {
	cfg     *config.Config
	client  *dv360.Client
	queries *db.Queries
}

func NewDV360Handler(cfg *config.Config, client *dv360.Client, queries *db.Queries) *DV360Handler {
	return &DV360Handler{cfg: cfg, client: client, queries: queries}
}

// Connect returns a same-origin start URL. The provider OAuth URL is never
// exposed to API clients; the browser is redirected by Start.
func (h *DV360Handler) Connect(c echo.Context) error {
	if !h.client.Configured() {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "programmatic integration not configured"})
	}
	startURL := c.Scheme() + "://" + c.Request().Host + "/api/integrations/programmatic/start"
	return c.JSON(http.StatusOK, map[string]string{"url": startURL})
}

// Start sets the CSRF state cookie and redirects the browser to the provider.
func (h *DV360Handler) Start(c echo.Context) error {
	if !h.client.Configured() {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "programmatic integration not configured"})
	}

	state := generateState()
	c.SetCookie(&http.Cookie{
		Name:     dv360StateCookie,
		Value:    state,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/api/integrations/programmatic",
		MaxAge:   600,
	})
	return c.Redirect(http.StatusFound, h.client.GetOAuthURL(state))
}

// Callback verifies state, exchanges the code (storing the refresh token),
// discovers every advertiser, and upserts them as ad accounts.
func (h *DV360Handler) Callback(c echo.Context) error {
	settingsURL := h.cfg.FrontendOrigin + "/settings/integrations"

	if c.QueryParam("error") != "" {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=programmatic_denied")
	}

	code := c.QueryParam("code")
	state := c.QueryParam("state")
	if code == "" {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=missing_code")
	}

	cookie, err := c.Cookie(dv360StateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != state {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=invalid_state")
	}
	h.clearStateCookie(c)

	workspaceID, _ := c.Get("workspace_id").(string)
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=unauthorized")
	}
	pgWID := pgtype.UUID{Bytes: wid, Valid: true}
	ctx := c.Request().Context()

	token, err := h.client.ExchangeCodeForToken(ctx, code)
	if err != nil {
		slog.Error("programmatic token exchange failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=token_exchange_failed")
	}

	advertisers, err := h.client.FetchAdvertisers(ctx, token.AccessToken)
	if err != nil || len(advertisers) == 0 {
		slog.Error("programmatic fetch advertisers failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=account_lookup_failed")
	}

	encrypted, err := crypto.EncryptToken(token.AccessToken, h.cfg.TokenEncryptionKey)
	if err != nil {
		slog.Error("programmatic token encryption failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=encryption_failed")
	}

	saved := h.upsertAdvertisers(ctx, pgWID, advertisers, encrypted)
	if saved == 0 {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=save_failed")
	}

	return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?connected=programmatic")
}

// Sync re-lists the connected Google user's advertisers and upserts them.
func (h *DV360Handler) Sync(c echo.Context) error {
	if !h.client.Configured() {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "programmatic integration not configured"})
	}

	wid, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	pgWID := pgtype.UUID{Bytes: wid, Valid: true}
	ctx := c.Request().Context()

	accounts, err := h.queries.GetAdAccountsByWorkspace(ctx, pgWID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to load integrations"})
	}

	var encrypted string
	for _, a := range accounts {
		if a.Platform == "dv360" {
			encrypted = a.AccessTokenEncrypted
			break
		}
	}
	if encrypted == "" {
		return badRequest(c, "programmatic is not connected")
	}

	token, err := crypto.DecryptToken(encrypted, h.cfg.TokenEncryptionKey)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to read credentials"})
	}

	advertisers, err := h.client.FetchAdvertisers(ctx, token)
	if err != nil {
		slog.Error("programmatic sync failed", "error", err)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to sync programmatic accounts"})
	}

	n := h.upsertAdvertisers(ctx, pgWID, advertisers, encrypted)
	return c.JSON(http.StatusOK, map[string]any{"accounts": n})
}

func (h *DV360Handler) upsertAdvertisers(ctx context.Context, workspaceID pgtype.UUID, advertisers []integrations.Account, encrypted string) int {
	saved := 0
	for _, acc := range advertisers {
		if _, err := h.queries.UpsertAdAccount(ctx, db.UpsertAdAccountParams{
			WorkspaceID:          workspaceID,
			Platform:             "dv360",
			ExternalAccountID:    acc.ID,
			AccessTokenEncrypted: encrypted,
			Name:                 textPg(acc.Name),
		}); err != nil {
			slog.Error("programmatic upsert ad account failed", "advertiser", acc.ID, "error", err)
			continue
		}
		saved++
	}
	return saved
}

func (h *DV360Handler) clearStateCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     dv360StateCookie,
		Value:    "",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/api/integrations/programmatic",
		MaxAge:   -1,
	})
}
