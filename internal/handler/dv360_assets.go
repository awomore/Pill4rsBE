package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

var errDV360AccountNotFound = errors.New("dv360 advertiser not found")

// Audiences lists an advertiser's audiences. ?type=first_party|google|combined|
// custom_list|lookalike.
func (h *DV360Handler) Audiences(c echo.Context) error {
	advertiserID := strings.TrimSpace(c.QueryParam("advertiser_id"))
	if advertiserID == "" {
		return badRequest(c, "advertiser_id is required")
	}
	token, err := h.dv360Token(c.Request().Context(), workspaceIDOf(c), advertiserID)
	if err != nil {
		return h.assetError(c, err)
	}

	page, err := h.client.ListAudiences(c.Request().Context(), token, advertiserID,
		c.QueryParam("type"), c.QueryParam("page_token"), intQuery(c, "page_size", 100))
	if err != nil {
		return h.upstreamError(c, err)
	}
	return c.JSON(http.StatusOK, page)
}

type createLookalikeRequest struct {
	AdvertiserID    string   `json:"advertiser_id"`
	Name            string   `json:"name"`
	ReachLevel      string   `json:"reach_level"`
	SeedAudienceIDs []string `json:"seed_audience_ids"`
	GeographyCodes  []string `json:"geography_codes"`
}

func (h *DV360Handler) CreateLookalike(c echo.Context) error {
	var req createLookalikeRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	if strings.TrimSpace(req.AdvertiserID) == "" {
		return badRequest(c, "advertiser_id is required")
	}
	token, err := h.dv360Token(c.Request().Context(), workspaceIDOf(c), req.AdvertiserID)
	if err != nil {
		return h.assetError(c, err)
	}

	audience, err := h.client.CreateLookalikeAudience(c.Request().Context(), token, req.AdvertiserID,
		req.Name, req.ReachLevel, req.SeedAudienceIDs, req.GeographyCodes)
	if err != nil {
		return h.upstreamError(c, err)
	}
	return c.JSON(http.StatusCreated, audience)
}

// CustomBiddingAlgorithms lists an advertiser's custom bidding algorithms.
func (h *DV360Handler) CustomBiddingAlgorithms(c echo.Context) error {
	advertiserID := strings.TrimSpace(c.QueryParam("advertiser_id"))
	if advertiserID == "" {
		return badRequest(c, "advertiser_id is required")
	}
	token, err := h.dv360Token(c.Request().Context(), workspaceIDOf(c), advertiserID)
	if err != nil {
		return h.assetError(c, err)
	}

	rows, next, err := h.client.ListCustomBiddingAlgorithms(c.Request().Context(), token, advertiserID,
		c.QueryParam("page_token"), intQuery(c, "page_size", 100))
	if err != nil {
		return h.upstreamError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"algorithms": rows, "next_page_token": next})
}

type createAlgorithmRequest struct {
	AdvertiserID string `json:"advertiser_id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
}

func (h *DV360Handler) CreateCustomBiddingAlgorithm(c echo.Context) error {
	var req createAlgorithmRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	if strings.TrimSpace(req.AdvertiserID) == "" {
		return badRequest(c, "advertiser_id is required")
	}
	token, err := h.dv360Token(c.Request().Context(), workspaceIDOf(c), req.AdvertiserID)
	if err != nil {
		return h.assetError(c, err)
	}

	algo, err := h.client.CreateCustomBiddingAlgorithm(c.Request().Context(), token, req.AdvertiserID, req.Name, req.Type)
	if err != nil {
		return h.upstreamError(c, err)
	}
	return c.JSON(http.StatusCreated, algo)
}

func (h *DV360Handler) CustomBiddingScripts(c echo.Context) error {
	advertiserID := strings.TrimSpace(c.QueryParam("advertiser_id"))
	if advertiserID == "" {
		return badRequest(c, "advertiser_id is required")
	}
	token, err := h.dv360Token(c.Request().Context(), workspaceIDOf(c), advertiserID)
	if err != nil {
		return h.assetError(c, err)
	}

	rows, next, err := h.client.ListCustomBiddingScripts(c.Request().Context(), token, advertiserID,
		c.Param("id"), c.QueryParam("page_token"), intQuery(c, "page_size", 100))
	if err != nil {
		return h.upstreamError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"scripts": rows, "next_page_token": next})
}

type uploadScriptRequest struct {
	AdvertiserID string `json:"advertiser_id"`
	Script       string `json:"script"`
}

func (h *DV360Handler) UploadCustomBiddingScript(c echo.Context) error {
	var req uploadScriptRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	if strings.TrimSpace(req.AdvertiserID) == "" {
		return badRequest(c, "advertiser_id is required")
	}
	token, err := h.dv360Token(c.Request().Context(), workspaceIDOf(c), req.AdvertiserID)
	if err != nil {
		return h.assetError(c, err)
	}

	script, err := h.client.UploadCustomBiddingScript(c.Request().Context(), token, req.AdvertiserID,
		c.Param("id"), req.Script)
	if err != nil {
		return h.upstreamError(c, err)
	}
	return c.JSON(http.StatusCreated, script)
}

func (h *DV360Handler) CustomBiddingRules(c echo.Context) error {
	advertiserID := strings.TrimSpace(c.QueryParam("advertiser_id"))
	if advertiserID == "" {
		return badRequest(c, "advertiser_id is required")
	}
	token, err := h.dv360Token(c.Request().Context(), workspaceIDOf(c), advertiserID)
	if err != nil {
		return h.assetError(c, err)
	}

	rows, next, err := h.client.ListCustomBiddingRules(c.Request().Context(), token, advertiserID,
		c.Param("id"), c.QueryParam("page_token"), intQuery(c, "page_size", 100))
	if err != nil {
		return h.upstreamError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"rules": rows, "next_page_token": next})
}

type uploadRulesRequest struct {
	AdvertiserID string          `json:"advertiser_id"`
	Rules        json.RawMessage `json:"rules"`
}

func (h *DV360Handler) UploadCustomBiddingRules(c echo.Context) error {
	var req uploadRulesRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	if strings.TrimSpace(req.AdvertiserID) == "" {
		return badRequest(c, "advertiser_id is required")
	}
	if len(req.Rules) == 0 {
		return badRequest(c, "rules is required")
	}
	token, err := h.dv360Token(c.Request().Context(), workspaceIDOf(c), req.AdvertiserID)
	if err != nil {
		return h.assetError(c, err)
	}

	rules, err := h.client.UploadCustomBiddingRules(c.Request().Context(), token, req.AdvertiserID,
		c.Param("id"), []byte(req.Rules))
	if err != nil {
		return h.upstreamError(c, err)
	}
	return c.JSON(http.StatusCreated, rules)
}

func (h *DV360Handler) dv360Token(ctx context.Context, workspaceID, advertiserID string) (string, error) {
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return "", errDV360AccountNotFound
	}
	accounts, err := h.queries.GetAdAccountsByWorkspace(ctx, pgtype.UUID{Bytes: wid, Valid: true})
	if err != nil {
		return "", err
	}
	for _, a := range accounts {
		if a.Platform == "dv360" && a.ExternalAccountID == advertiserID {
			return crypto.DecryptToken(a.AccessTokenEncrypted, h.cfg.TokenEncryptionKey)
		}
	}
	return "", errDV360AccountNotFound
}

func (h *DV360Handler) assetError(c echo.Context, err error) error {
	if errors.Is(err, errDV360AccountNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "programmatic advertiser not found in this workspace"})
	}
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to read programmatic credentials"})
}

func intQuery(c echo.Context, name string, fallback int) int {
	n, err := strconv.Atoi(c.QueryParam(name))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func (h *DV360Handler) upstreamError(c echo.Context, err error) error {
	slog.Error("programmatic upstream error", "error", err)
	return c.JSON(http.StatusBadGateway, map[string]string{"error": "programmatic request failed"})
}

type createAdvertiserRequest struct {
	Name         string `json:"name"`
	DomainURL    string `json:"domain_url"`
	CurrencyCode string `json:"currency_code"`
}

// ManagedAdvertiser creates a DSP advertiser under the Pill4rs partner and
// attaches it to the workspace. This is the provisioning step that lets an
// agency run programmatic campaigns for a client through Pill4rs.
func (h *DV360Handler) ManagedAdvertiser(c echo.Context) error {
	if h.cfg.GoogleClientID == "" || h.cfg.GoogleClientSecret == "" || h.cfg.DV360PartnerID == "" || h.cfg.DV360BillingProfileID == "" || h.cfg.DV360ManagedRefreshToken == "" {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "managed programmatic advertisers are not configured"})
	}
	wid, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	var req createAdvertiserRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	name := strings.TrimSpace(req.Name)
	domain := strings.TrimSpace(req.DomainURL)
	currency := strings.ToUpper(strings.TrimSpace(req.CurrencyCode))
	if name == "" || domain == "" || len(currency) != 3 {
		return badRequest(c, "name, domain_url, and a 3-letter currency_code are required")
	}
	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		return badRequest(c, "domain_url must start with http:// or https://")
	}

	ctx := c.Request().Context()
	advertiserID, err := h.client.CreateAdvertiser(ctx, h.cfg.DV360ManagedRefreshToken,
		h.cfg.DV360PartnerID, h.cfg.DV360BillingProfileID, name, domain, currency)
	if err != nil {
		slog.Error("programmatic advertiser creation failed", "error", err)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to create programmatic advertiser"})
	}

	encrypted, err := crypto.EncryptToken(h.cfg.DV360ManagedRefreshToken, h.cfg.TokenEncryptionKey)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to store credentials"})
	}
	row, err := h.queries.UpsertAdAccount(ctx, db.UpsertAdAccountParams{
		WorkspaceID:          pgtype.UUID{Bytes: wid, Valid: true},
		Platform:             "dv360",
		ExternalAccountID:    advertiserID,
		AccessTokenEncrypted: encrypted,
		Name:                 pgtype.Text{String: name, Valid: true},
	})
	if err != nil {
		slog.Error("programmatic advertiser save failed", "advertiser", advertiserID, "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to save programmatic advertiser"})
	}
	updated, err := h.queries.SetAdAccountBilling(ctx, db.SetAdAccountBillingParams{
		ID:          row.ID,
		BillingMode: "managed",
		Payer:       "pill4rs",
		Currency:    currency,
	})
	if err != nil {
		slog.Error("programmatic advertiser billing setup failed", "advertiser", advertiserID, "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to configure billing"})
	}

	return c.JSON(http.StatusCreated, map[string]any{
		"id":                  formatUUID(updated.ID),
		"platform":            "programmatic",
		"external_account_id": updated.ExternalAccountID,
		"name":                name,
		"billing_mode":        updated.BillingMode,
		"payer":               updated.Payer,
		"currency":            updated.Currency,
	})
}
