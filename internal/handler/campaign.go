package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/integrations"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type CampaignHandler struct {
	svc *service.CampaignService
}

func NewCampaignHandler(svc *service.CampaignService) *CampaignHandler {
	return &CampaignHandler{svc: svc}
}

type createCampaignRequest struct {
	Name        string           `json:"name"`
	Objective   string           `json:"objective"`
	DailyBudget float64          `json:"daily_budget"`
	Currency    string           `json:"currency"`
	StartDate   string           `json:"start_date"`
	EndDate     string           `json:"end_date"`
	CTA         string           `json:"cta"`
	Platforms   []string         `json:"platforms"`
	Targeting   map[string]any   `json:"targeting"`
	Creative    *creativeRequest `json:"creative"`
}

type creativeRequest struct {
	PrimaryText string `json:"primary_text"`
	Headline    string `json:"headline"`
	Description string `json:"description"`
	LinkURL     string `json:"link_url"`
	ImageURL    string `json:"image_url"`
}

func toCreativeSpec(r *creativeRequest) *integrations.CreativeSpec {
	if r == nil {
		return nil
	}
	return &integrations.CreativeSpec{
		PrimaryText: r.PrimaryText,
		Headline:    r.Headline,
		Description: r.Description,
		LinkURL:     r.LinkURL,
		ImageURL:    r.ImageURL,
	}
}

// Create fans one campaign spec out to every requested platform. Returns the
// created campaigns plus any per-platform warnings; only 4xx if nothing was made.
func (h *CampaignHandler) Create(c echo.Context) error {
	workspaceID, _ := c.Get("workspace_id").(string)
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}

	var req createCampaignRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}

	start, err := parseOptionalDate(req.StartDate)
	if err != nil {
		return badRequest(c, "start_date must be in YYYY-MM-DD format")
	}
	end, err := parseOptionalDate(req.EndDate)
	if err != nil {
		return badRequest(c, "end_date must be in YYYY-MM-DD format")
	}

	input := service.CreateCampaignInput{
		Name:        req.Name,
		Objective:   req.Objective,
		DailyBudget: req.DailyBudget,
		Currency:    req.Currency,
		StartDate:   start,
		EndDate:     end,
		CTA:         req.CTA,
		Platforms:   req.Platforms,
		Targeting:   req.Targeting,
		Creative:    toCreativeSpec(req.Creative),
	}

	result, err := h.svc.CreateCampaign(c.Request().Context(), wid, input)
	if err != nil {
		var ve service.ValidationError
		switch {
		case errors.As(err, &ve):
			return badRequest(c, ve.Msg)
		case errors.Is(err, service.ErrNoCampaignsCreated):
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error":    "no campaigns could be created — connect a platform first",
				"warnings": warningsOrEmpty(result.Warnings),
			})
		default:
			slog.Error("create campaign failed", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create campaign"})
		}
	}

	campaigns := make([]map[string]interface{}, 0, len(result.Created))
	for _, cc := range result.Created {
		campaigns = append(campaigns, campaignToMap(cc.Campaign, cc.Platform))
	}
	return c.JSON(http.StatusCreated, map[string]interface{}{
		"campaigns": campaigns,
		"warnings":  warningsOrEmpty(result.Warnings),
	})
}

// Launch pushes a saved DRAFT live (re-runs the remote create, flips to PAUSED).
func (h *CampaignHandler) Launch(c echo.Context) error {
	workspaceID, _ := c.Get("workspace_id").(string)
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	cid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid campaign id")
	}

	camp, platform, err := h.svc.LaunchCampaign(c.Request().Context(), wid, cid)
	if err != nil {
		var ve service.ValidationError
		switch {
		case errors.Is(err, service.ErrCampaignNotFound):
			return c.JSON(http.StatusNotFound, map[string]string{"error": "campaign not found"})
		case errors.As(err, &ve):
			return badRequest(c, ve.Msg)
		default:
			slog.Error("launch campaign failed", "error", err)
			return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to launch campaign on the platform"})
		}
	}
	return c.JSON(http.StatusOK, campaignToMap(camp, platform))
}

func campaignToMap(camp db.Campaign, platform string) map[string]interface{} {
	m := map[string]interface{}{
		"id":                   formatUUID(camp.ID),
		"platform":             platform,
		"external_campaign_id": camp.ExternalCampaignID,
		"name":                 camp.Name,
		"status":               camp.Status,
		"is_draft":             camp.Status == service.StatusDraft,
	}
	if camp.Objective.Valid {
		m["objective"] = camp.Objective.String
	}
	if b, ok := numericToFloat(camp.DailyBudget); ok {
		m["daily_budget"] = b
	}
	if camp.StartDate.Valid {
		m["start_date"] = camp.StartDate.Time.Format("2006-01-02")
	}
	if camp.EndDate.Valid {
		m["end_date"] = camp.EndDate.Time.Format("2006-01-02")
	}
	if camp.Cta.Valid {
		m["cta"] = camp.Cta.String
	}
	if len(camp.Targeting) > 0 {
		m["targeting"] = json.RawMessage(camp.Targeting)
	}
	if len(camp.Creative) > 0 {
		m["creative"] = json.RawMessage(camp.Creative)
	}
	if camp.ExternalAdsetID.Valid {
		m["external_adset_id"] = camp.ExternalAdsetID.String
	}
	if camp.ExternalAdID.Valid {
		m["external_ad_id"] = camp.ExternalAdID.String
	}
	if camp.ExternalCreativeID.Valid {
		m["external_creative_id"] = camp.ExternalCreativeID.String
	}
	if camp.CreatedAt.Valid {
		m["created_at"] = camp.CreatedAt.Time
	}
	if camp.UpdatedAt.Valid {
		m["updated_at"] = camp.UpdatedAt.Time
	}
	return m
}

func parseOptionalDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse("2006-01-02", s)
}

func warningsOrEmpty(w []string) []string {
	if w == nil {
		return []string{}
	}
	return w
}
