package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/awomore/Pill4rsBE/internal/config"
	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/integrations/google"
	"github.com/awomore/Pill4rsBE/internal/integrations/meta"
	"github.com/awomore/Pill4rsBE/internal/integrations/tiktok"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

const (
	metaStateCookie   = "meta_oauth_state"
	tiktokStateCookie = "tiktok_oauth_state"
	googleStateCookie = "google_ads_oauth_state"
)

type IntegrationsHandler struct {
	queries *db.Queries
	cfg     *config.Config
	meta    *meta.Client
	tiktok  *tiktok.Client
	google  *google.Client
}

func NewIntegrationsHandler(queries *db.Queries, cfg *config.Config, metaClient *meta.Client, tiktokClient *tiktok.Client, googleClient *google.Client) *IntegrationsHandler {
	return &IntegrationsHandler{queries: queries, cfg: cfg, meta: metaClient, tiktok: tiktokClient, google: googleClient}
}

// MetaConnect generates a CSRF state, stores it in a short-lived HttpOnly
// cookie, and returns the Meta OAuth dialog URL.
func (h *IntegrationsHandler) MetaConnect(c echo.Context) error {
	if h.cfg.MetaAppID == "" {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "meta integration not configured"})
	}

	state := generateState()
	c.SetCookie(&http.Cookie{
		Name:     metaStateCookie,
		Value:    state,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/api/integrations/meta",
		MaxAge:   600,
	})

	return c.JSON(http.StatusOK, map[string]string{"url": h.meta.GetOAuthURL(state)})
}

// MetaCallback verifies the state, exchanges the code, encrypts the token,
// upserts the ad_accounts row, and redirects the browser to the frontend.
func (h *IntegrationsHandler) MetaCallback(c echo.Context) error {
	settingsURL := h.cfg.FrontendOrigin + "/settings/integrations"

	if c.QueryParam("error") != "" {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=meta_denied")
	}

	code := c.QueryParam("code")
	state := c.QueryParam("state")
	if code == "" {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=missing_code")
	}

	cookie, err := c.Cookie(metaStateCookie)
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

	token, err := h.meta.ExchangeCodeForToken(ctx, code)
	if err != nil {
		slog.Error("meta token exchange failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=token_exchange_failed")
	}

	accounts, err := h.meta.FetchAdAccounts(ctx, token.AccessToken)
	if err != nil || len(accounts) == 0 {
		slog.Error("meta fetch ad accounts failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=account_lookup_failed")
	}

	encrypted, err := crypto.EncryptToken(token.AccessToken, h.cfg.TokenEncryptionKey)
	if err != nil {
		slog.Error("meta token encryption failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=encryption_failed")
	}

	var expiry pgtype.Timestamptz
	if !token.ExpiresAt.IsZero() {
		expiry = pgtype.Timestamptz{Time: token.ExpiresAt, Valid: true}
	}

	saved := 0
	for _, acc := range accounts {
		row, err := h.queries.UpsertAdAccount(ctx, db.UpsertAdAccountParams{
			WorkspaceID:          pgWID,
			Platform:             "meta",
			ExternalAccountID:    acc.ID,
			AccessTokenEncrypted: encrypted,
			TokenExpiresAt:       expiry,
			Name:                 textPg(acc.Name),
		})
		if err != nil {
			slog.Error("meta upsert ad account failed", "account", acc.ID, "error", err)
			continue
		}
		saved++
		// Best-effort: capture a default page + pixel per account so delivery
		// works out of the box; the user can override via the configure endpoint.
		h.autoCaptureMetaAssets(ctx, row.ID, token.AccessToken, acc.ID)
	}
	if saved == 0 {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=save_failed")
	}

	return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?connected=meta")
}

func (h *IntegrationsHandler) autoCaptureMetaAssets(ctx context.Context, accountRowID pgtype.UUID, accessToken, externalAccountID string) {
	var pageID, pixelID pgtype.Text
	if pages, err := h.meta.FetchPages(ctx, accessToken); err == nil && len(pages) > 0 {
		pageID = pgtype.Text{String: pages[0].ID, Valid: true}
	}
	if pixels, err := h.meta.FetchPixels(ctx, accessToken, externalAccountID); err == nil && len(pixels) > 0 {
		pixelID = pgtype.Text{String: pixels[0].ID, Valid: true}
	}
	if !pageID.Valid && !pixelID.Valid {
		return
	}
	if _, err := h.queries.SetAdAccountPageAndPixel(ctx, db.SetAdAccountPageAndPixelParams{
		ID:      accountRowID,
		PageID:  pageID,
		PixelID: pixelID,
	}); err != nil {
		slog.Warn("meta page/pixel auto-capture failed", "error", err)
	}
}

// List returns the workspace's connected ad accounts without exposing tokens.
func (h *IntegrationsHandler) List(c echo.Context) error {
	workspaceID, _ := c.Get("workspace_id").(string)
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid workspace id"})
	}
	pgWID := pgtype.UUID{Bytes: wid, Valid: true}

	accounts, err := h.queries.GetAdAccountsByWorkspace(c.Request().Context(), pgWID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list integrations"})
	}

	out := make([]map[string]interface{}, 0, len(accounts))
	for _, a := range accounts {
		item := map[string]interface{}{
			"id":                  formatUUID(a.ID),
			"platform":            a.Platform,
			"external_account_id": a.ExternalAccountID,
			"status":              a.Status,
		}
		if a.Name.Valid {
			item["name"] = a.Name.String
		}
		if a.ConnectedAt.Valid {
			item["connected_at"] = a.ConnectedAt.Time
		}
		if a.PageID.Valid {
			item["page_id"] = a.PageID.String
		}
		if a.PixelID.Valid {
			item["pixel_id"] = a.PixelID.String
		}
		out = append(out, item)
	}
	return c.JSON(http.StatusOK, out)
}

// Disconnect removes a single connected ad account by id.
func (h *IntegrationsHandler) Disconnect(c echo.Context) error {
	wid, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid workspace id"})
	}
	aid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid account id"})
	}

	if err := h.queries.DeleteAdAccountForWorkspace(c.Request().Context(), db.DeleteAdAccountForWorkspaceParams{
		ID:          pgtype.UUID{Bytes: aid, Valid: true},
		WorkspaceID: pgtype.UUID{Bytes: wid, Valid: true},
	}); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to disconnect"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// AccountOptions lists a Meta account's Facebook pages and pixels so the user
// can pick which to use for ad delivery (account id in the path).
func (h *IntegrationsHandler) AccountOptions(c echo.Context) error {
	wid, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid workspace id"})
	}
	aid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid account id"})
	}
	ctx := c.Request().Context()

	account, err := h.getAccountForWorkspace(ctx, wid, aid)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "account not found"})
	}
	if account.Platform != "meta" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "page/pixel options are only available for meta accounts"})
	}

	token, err := crypto.DecryptToken(account.AccessTokenEncrypted, h.cfg.TokenEncryptionKey)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to read credentials"})
	}

	pages, err := h.meta.FetchPages(ctx, token)
	if err != nil {
		slog.Error("meta fetch pages failed", "error", err)
	}
	pixels, err := h.meta.FetchPixels(ctx, token, account.ExternalAccountID)
	if err != nil {
		slog.Error("meta fetch pixels failed", "error", err)
	}

	pageList := make([]map[string]string, 0, len(pages))
	for _, p := range pages {
		pageList = append(pageList, map[string]string{"id": p.ID, "name": p.Name})
	}
	pixelList := make([]map[string]string, 0, len(pixels))
	for _, p := range pixels {
		pixelList = append(pixelList, map[string]string{"id": p.ID, "name": p.Name})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"selected": map[string]string{
			"page_id":  textOrEmpty(account.PageID),
			"pixel_id": textOrEmpty(account.PixelID),
		},
		"pages":  pageList,
		"pixels": pixelList,
	})
}

type metaConfigureRequest struct {
	PageID  *string `json:"page_id"`
	PixelID *string `json:"pixel_id"`
}

// AccountConfigure sets the page_id / pixel_id for a Meta account (id in path).
func (h *IntegrationsHandler) AccountConfigure(c echo.Context) error {
	wid, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid workspace id"})
	}
	aid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid account id"})
	}
	ctx := c.Request().Context()

	var req metaConfigureRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	account, err := h.getAccountForWorkspace(ctx, wid, aid)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "account not found"})
	}
	if account.Platform != "meta" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "page/pixel can only be set on meta accounts"})
	}

	pageID := account.PageID
	pixelID := account.PixelID
	if req.PageID != nil {
		pageID = pgtype.Text{String: *req.PageID, Valid: *req.PageID != ""}
	}
	if req.PixelID != nil {
		pixelID = pgtype.Text{String: *req.PixelID, Valid: *req.PixelID != ""}
	}

	updated, err := h.queries.SetAdAccountPageAndPixel(ctx, db.SetAdAccountPageAndPixelParams{
		ID:      account.ID,
		PageID:  pageID,
		PixelID: pixelID,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update integration"})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"id":       formatUUID(updated.ID),
		"platform": updated.Platform,
		"status":   updated.Status,
		"page_id":  textOrEmpty(updated.PageID),
		"pixel_id": textOrEmpty(updated.PixelID),
	})
}

func (h *IntegrationsHandler) getAccountForWorkspace(ctx context.Context, workspaceID, accountID uuid.UUID) (db.AdAccount, error) {
	return h.queries.GetAdAccountForWorkspace(ctx, db.GetAdAccountForWorkspaceParams{
		ID:          pgtype.UUID{Bytes: accountID, Valid: true},
		WorkspaceID: pgtype.UUID{Bytes: workspaceID, Valid: true},
	})
}

func textOrEmpty(t pgtype.Text) string {
	if t.Valid {
		return t.String
	}
	return ""
}

func textPg(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// TikTokConnect returns the TikTok OAuth URL and stores a CSRF state cookie.
func (h *IntegrationsHandler) TikTokConnect(c echo.Context) error {
	if h.cfg.TikTokAppID == "" {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "tiktok integration not configured"})
	}

	state := generateState()
	c.SetCookie(&http.Cookie{
		Name:     tiktokStateCookie,
		Value:    state,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/api/integrations/tiktok",
		MaxAge:   600,
	})

	return c.JSON(http.StatusOK, map[string]string{"url": h.tiktok.GetOAuthURL(state)})
}

// TikTokCallback verifies state, exchanges the code, and stores the ad account.
func (h *IntegrationsHandler) TikTokCallback(c echo.Context) error {
	settingsURL := h.cfg.FrontendOrigin + "/settings/integrations"

	if c.QueryParam("error") != "" {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=tiktok_denied")
	}

	// TikTok returns the authorization code as `auth_code` (sometimes `code`).
	code := c.QueryParam("auth_code")
	if code == "" {
		code = c.QueryParam("code")
	}
	state := c.QueryParam("state")
	if code == "" {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=missing_code")
	}

	cookie, err := c.Cookie(tiktokStateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != state {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=invalid_state")
	}
	c.SetCookie(&http.Cookie{
		Name: tiktokStateCookie, Value: "", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode, Path: "/api/integrations/tiktok", MaxAge: -1,
	})

	workspaceID, _ := c.Get("workspace_id").(string)
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=unauthorized")
	}
	pgWID := pgtype.UUID{Bytes: wid, Valid: true}
	ctx := c.Request().Context()

	token, err := h.tiktok.ExchangeCodeForToken(ctx, code)
	if err != nil {
		slog.Error("tiktok token exchange failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=token_exchange_failed")
	}

	accounts, err := h.tiktok.FetchAdvertisers(ctx, token.AccessToken)
	if err != nil || len(accounts) == 0 {
		slog.Error("tiktok fetch advertisers failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=account_lookup_failed")
	}

	encrypted, err := crypto.EncryptToken(token.AccessToken, h.cfg.TokenEncryptionKey)
	if err != nil {
		slog.Error("tiktok token encryption failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=encryption_failed")
	}

	saved := 0
	for _, acc := range accounts {
		if _, err := h.queries.UpsertAdAccount(ctx, db.UpsertAdAccountParams{
			WorkspaceID:          pgWID,
			Platform:             "tiktok",
			ExternalAccountID:    acc.ID,
			AccessTokenEncrypted: encrypted,
			Name:                 textPg(acc.Name),
		}); err != nil {
			slog.Error("tiktok upsert ad account failed", "account", acc.ID, "error", err)
			continue
		}
		saved++
	}
	if saved == 0 {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=save_failed")
	}

	return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?connected=tiktok")
}

// GoogleConnect returns the Google Ads OAuth URL and stores a CSRF state cookie.
func (h *IntegrationsHandler) GoogleConnect(c echo.Context) error {
	if h.cfg.GoogleClientID == "" || h.cfg.GoogleAdsDeveloperToken == "" {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "google ads integration not configured"})
	}

	state := generateState()
	c.SetCookie(&http.Cookie{
		Name:     googleStateCookie,
		Value:    state,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/api/integrations/google",
		MaxAge:   600,
	})

	return c.JSON(http.StatusOK, map[string]string{"url": h.google.GetOAuthURL(state)})
}

// GoogleCallback verifies state, exchanges the code (storing the refresh token),
// resolves the customer id, and stores the ad account.
func (h *IntegrationsHandler) GoogleCallback(c echo.Context) error {
	settingsURL := h.cfg.FrontendOrigin + "/settings/integrations"

	if c.QueryParam("error") != "" {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=google_denied")
	}

	code := c.QueryParam("code")
	state := c.QueryParam("state")
	if code == "" {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=missing_code")
	}

	cookie, err := c.Cookie(googleStateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != state {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=invalid_state")
	}
	c.SetCookie(&http.Cookie{
		Name: googleStateCookie, Value: "", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode, Path: "/api/integrations/google", MaxAge: -1,
	})

	workspaceID, _ := c.Get("workspace_id").(string)
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=unauthorized")
	}
	pgWID := pgtype.UUID{Bytes: wid, Valid: true}
	ctx := c.Request().Context()

	// token.AccessToken is the long-lived refresh token (Google convention).
	token, err := h.google.ExchangeCodeForToken(ctx, code)
	if err != nil {
		slog.Error("google token exchange failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=token_exchange_failed")
	}

	accounts, err := h.google.FetchCustomers(ctx, token.AccessToken)
	if err != nil || len(accounts) == 0 {
		slog.Error("google fetch customers failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=account_lookup_failed")
	}

	encrypted, err := crypto.EncryptToken(token.AccessToken, h.cfg.TokenEncryptionKey)
	if err != nil {
		slog.Error("google token encryption failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=encryption_failed")
	}

	saved := 0
	for _, acc := range accounts {
		if _, err := h.queries.UpsertAdAccount(ctx, db.UpsertAdAccountParams{
			WorkspaceID:          pgWID,
			Platform:             "google",
			ExternalAccountID:    acc.ID,
			AccessTokenEncrypted: encrypted,
			Name:                 textPg(acc.Name),
		}); err != nil {
			slog.Error("google upsert ad account failed", "account", acc.ID, "error", err)
			continue
		}
		saved++
	}
	if saved == 0 {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=save_failed")
	}

	return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?connected=google")
}

func (h *IntegrationsHandler) clearStateCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     metaStateCookie,
		Value:    "",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/api/integrations/meta",
		MaxAge:   -1,
	})
}
