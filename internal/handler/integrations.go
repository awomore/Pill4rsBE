package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/awomore/Pill4rsBE/internal/config"
	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/integrations/meta"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

const metaStateCookie = "meta_oauth_state"

var errMetaNotConnected = errors.New("meta not connected")

type IntegrationsHandler struct {
	queries *db.Queries
	cfg     *config.Config
	meta    *meta.Client
}

func NewIntegrationsHandler(queries *db.Queries, cfg *config.Config, metaClient *meta.Client) *IntegrationsHandler {
	return &IntegrationsHandler{queries: queries, cfg: cfg, meta: metaClient}
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

	accountID, err := h.meta.FetchAdAccountID(ctx, token.AccessToken)
	if err != nil {
		slog.Error("meta fetch ad account failed", "error", err)
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

	account, err := h.queries.UpsertAdAccount(ctx, db.UpsertAdAccountParams{
		WorkspaceID:          pgWID,
		Platform:             "meta",
		ExternalAccountID:    accountID,
		AccessTokenEncrypted: encrypted,
		TokenExpiresAt:       expiry,
	})
	if err != nil {
		slog.Error("meta upsert ad account failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=save_failed")
	}

	// Best-effort: capture a default page + pixel so campaign delivery works
	// out of the box. The user can override these via PATCH /api/integrations/meta.
	h.autoCaptureMetaAssets(ctx, account.ID, token.AccessToken, accountID)

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
			"id":       formatUUID(a.ID),
			"platform": a.Platform,
			"status":   a.Status,
		}
		if a.ConnectedAt.Valid {
			item["connected_at"] = a.ConnectedAt.Time
		}
		out = append(out, item)
	}
	return c.JSON(http.StatusOK, out)
}

// MetaDisconnect deletes the workspace's Meta ad account row.
func (h *IntegrationsHandler) MetaDisconnect(c echo.Context) error {
	workspaceID, _ := c.Get("workspace_id").(string)
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid workspace id"})
	}
	pgWID := pgtype.UUID{Bytes: wid, Valid: true}

	err = h.queries.DeleteAdAccountByWorkspaceAndPlatform(c.Request().Context(), db.DeleteAdAccountByWorkspaceAndPlatformParams{
		WorkspaceID: pgWID,
		Platform:    "meta",
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to disconnect"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// MetaOptions lists the Facebook pages and pixels available on the connected
// Meta account so the user can pick which to use for ad delivery.
func (h *IntegrationsHandler) MetaOptions(c echo.Context) error {
	workspaceID, _ := c.Get("workspace_id").(string)
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid workspace id"})
	}
	pgWID := pgtype.UUID{Bytes: wid, Valid: true}
	ctx := c.Request().Context()

	account, err := h.getMetaAccount(ctx, pgWID)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "meta is not connected"})
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

// MetaConfigure sets the page_id / pixel_id used for the connected Meta account.
func (h *IntegrationsHandler) MetaConfigure(c echo.Context) error {
	workspaceID, _ := c.Get("workspace_id").(string)
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid workspace id"})
	}
	pgWID := pgtype.UUID{Bytes: wid, Valid: true}
	ctx := c.Request().Context()

	var req metaConfigureRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	account, err := h.getMetaAccount(ctx, pgWID)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "meta is not connected"})
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

func (h *IntegrationsHandler) getMetaAccount(ctx context.Context, workspaceID pgtype.UUID) (db.AdAccount, error) {
	accounts, err := h.queries.GetAdAccountsByWorkspace(ctx, workspaceID)
	if err != nil {
		return db.AdAccount{}, err
	}
	for _, a := range accounts {
		if a.Platform == "meta" {
			return a, nil
		}
	}
	return db.AdAccount{}, errMetaNotConnected
}

func textOrEmpty(t pgtype.Text) string {
	if t.Valid {
		return t.String
	}
	return ""
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
