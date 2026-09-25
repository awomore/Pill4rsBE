package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/config"
	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/integrations/zernio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

// ZernioHandler connects a workspace's ad accounts through Zernio and syncs
// them into Pill4rs as ad_accounts, so the campaign engine and Oma can control
// every network Zernio supports.
type ZernioHandler struct {
	cfg     *config.Config
	client  *zernio.Client
	queries *db.Queries
}

func NewZernioHandler(cfg *config.Config, client *zernio.Client, queries *db.Queries) *ZernioHandler {
	return &ZernioHandler{cfg: cfg, client: client, queries: queries}
}

// Connect starts the Zernio unified ads OAuth for a posting platform
// (facebook, instagram, linkedin, tiktok, twitter, pinterest, googleads) and
// returns the authorization URL. Zernio completes OAuth on its own callback and
// redirects the browser to our callback, which syncs the connected accounts.
func (h *ZernioHandler) Connect(c echo.Context) error {
	if !h.client.Configured() {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "zernio is not configured"})
	}
	if _, err := uuid.Parse(workspaceIDOf(c)); err != nil {
		return badRequest(c, "invalid workspace id")
	}

	platform := c.QueryParam("platform")
	if platform == "" {
		return badRequest(c, "platform is required (e.g. linkedin, twitter, pinterest, googleads)")
	}
	profileID := c.QueryParam("profile_id")
	if profileID == "" {
		resolved, perr := h.resolveProfileID(c.Request().Context(), workspaceIDOf(c))
		if perr != nil {
			slog.Error("zernio profile resolve failed", "error", perr)
			return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to start zernio connection"})
		}
		profileID = resolved
	}

	callback := c.Scheme() + "://" + c.Request().Host + "/api/integrations/zernio/callback"
	if profileID != "" {
		callback += "/" + profileID
	}

	res, err := h.client.AdsConnectURL(c.Request().Context(), platform, profileID, callback)
	if err != nil {
		slog.Error("zernio connect failed", "platform", platform, "error", err)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to start zernio connection"})
	}
	if res.AlreadyConnected {
		if n, serr := h.syncAccounts(c.Request().Context(), workspaceIDOf(c), profileID); serr == nil {
			return c.JSON(http.StatusOK, map[string]any{"already_connected": true, "accounts": n})
		}
		return c.JSON(http.StatusOK, map[string]any{"already_connected": true})
	}
	return c.JSON(http.StatusOK, map[string]string{"auth_url": res.AuthURL, "state": res.State})
}

// Callback is where Zernio redirects the browser after OAuth. It syncs the
// profile's accounts into the workspace, then sends the user back to the app.
func (h *ZernioHandler) Callback(c echo.Context) error {
	settingsURL := h.cfg.FrontendOrigin + "/settings/integrations"
	if c.QueryParam("error") != "" {
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=zernio_denied")
	}
	profileID := c.Param("profile_id")
	if profileID == "" {
		profileID = c.QueryParam("profileId")
	}
	if profileID == "" {
		if resolved, perr := h.resolveProfileID(c.Request().Context(), workspaceIDOf(c)); perr == nil {
			profileID = resolved
		}
	}
	if _, err := h.syncAccounts(c.Request().Context(), workspaceIDOf(c), profileID); err != nil {
		slog.Error("zernio callback sync failed", "error", err)
		return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?error=zernio_sync_failed")
	}
	return c.Redirect(http.StatusTemporaryRedirect, settingsURL+"?connected=zernio")
}

// Sync re-lists the profile's Zernio accounts and upserts them as ad accounts.
func (h *ZernioHandler) Sync(c echo.Context) error {
	if !h.client.Configured() {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "zernio is not configured"})
	}
	profileID := c.QueryParam("profile_id")
	if profileID == "" {
		resolved, perr := h.resolveProfileID(c.Request().Context(), workspaceIDOf(c))
		if perr != nil {
			slog.Error("zernio profile resolve failed", "error", perr)
			return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to sync zernio accounts"})
		}
		profileID = resolved
	}
	n, err := h.syncAccounts(c.Request().Context(), workspaceIDOf(c), profileID)
	if err != nil {
		slog.Error("zernio sync failed", "error", err)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to sync zernio accounts"})
	}
	return c.JSON(http.StatusOK, map[string]any{"accounts": n})
}

// resolveProfileID returns the workspace's Zernio profile id, creating one on
// first use. Zernio requires a profileId to start an ads connection, so each
// Pill4rs workspace maps to exactly one Zernio profile.
func (h *ZernioHandler) resolveProfileID(ctx context.Context, workspaceID string) (string, error) {
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return "", err
	}
	pgWID := pgtype.UUID{Bytes: wid, Valid: true}

	ws, err := h.queries.GetWorkspaceByID(ctx, pgWID)
	if err != nil {
		return "", err
	}
	if ws.ZernioProfileID.Valid && ws.ZernioProfileID.String != "" {
		return ws.ZernioProfileID.String, nil
	}

	name := ws.BusinessName.String
	if !ws.BusinessName.Valid || strings.TrimSpace(name) == "" {
		name = "Pill4rs workspace " + wid.String()
	}
	profile, err := h.client.CreateProfile(ctx, name)
	if err != nil {
		return "", err
	}
	if profile.ID == "" {
		return "", fmt.Errorf("zernio profile create returned no id")
	}
	if _, err := h.queries.SetWorkspaceZernioProfile(ctx, db.SetWorkspaceZernioProfileParams{
		ID:              pgWID,
		ZernioProfileID: pgtype.Text{String: profile.ID, Valid: true},
	}); err != nil {
		return "", err
	}
	return profile.ID, nil
}

// syncAccounts upserts every Zernio ads account for a profile as a Pill4rs
// ad_account, storing the encrypted Zernio API key as the account token.
func (h *ZernioHandler) syncAccounts(ctx context.Context, workspaceID, profileID string) (int, error) {
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return 0, err
	}
	accounts, err := h.client.ListAccounts(ctx, profileID)
	if err != nil {
		return 0, err
	}
	encrypted, err := crypto.EncryptToken(h.cfg.ZernioAPIKey, h.cfg.TokenEncryptionKey)
	if err != nil {
		return 0, err
	}

	pgWID := pgtype.UUID{Bytes: wid, Valid: true}
	saved := 0
	for _, a := range accounts {
		if !zernio.IsAdsPlatform(a.Platform) {
			continue
		}
		name := a.DisplayName
		if name == "" {
			name = a.Username
		}
		if _, err := h.queries.UpsertAdAccount(ctx, db.UpsertAdAccountParams{
			WorkspaceID:          pgWID,
			Platform:             a.Platform,
			ExternalAccountID:    a.ID,
			AccessTokenEncrypted: encrypted,
			Name:                 textPg(name),
		}); err != nil {
			slog.Error("zernio upsert ad account failed", "account", a.ID, "error", err)
			continue
		}
		saved++
	}
	return saved, nil
}
