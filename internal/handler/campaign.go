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
	svc     *service.CampaignService
	actions *service.ActionService
}

func NewCampaignHandler(svc *service.CampaignService, actions *service.ActionService) *CampaignHandler {
	return &CampaignHandler{svc: svc, actions: actions}
}

type createCampaignRequest struct {
	Name             string            `json:"name"`
	Objective        string            `json:"objective"`
	DailyBudget      float64           `json:"daily_budget"`
	Currency         string            `json:"currency"`
	StartDate        string            `json:"start_date"`
	EndDate          string            `json:"end_date"`
	CTA              string            `json:"cta"`
	AdAccountIDs     []string          `json:"ad_account_ids"`
	Platforms        []string          `json:"platforms"`
	BidStrategy      string            `json:"bid_strategy,omitempty"`
	BidCap           float64           `json:"bid_cap,omitempty"`
	PacingType       string            `json:"pacing_type,omitempty"`
	FrequencyCap     int               `json:"frequency_cap,omitempty"`
	FrequencyCapUnit string            `json:"frequency_cap_time_unit,omitempty"`
	Targeting        map[string]any    `json:"targeting,omitempty"`
	Creative         *creativeRequest  `json:"creative,omitempty"`
	Variants         []variantRequest  `json:"variants,omitempty"`
	Provenance       map[string]string `json:"provenance,omitempty"`
	Rationale        map[string]string `json:"rationale,omitempty"`
}

type variantRequest struct {
	Targeting map[string]any   `json:"targeting"`
	Creative  *creativeRequest `json:"creative"`
}

type creativeRequest struct {
	PrimaryText  string `json:"primary_text"`
	Headline     string `json:"headline"`
	Description  string `json:"description"`
	LinkURL      string `json:"link_url"`
	ImageURL     string `json:"image_url"`
	VideoURL     string `json:"video_url,omitempty"`
	Format       string `json:"format,omitempty"`
	MediaAssetID string `json:"media_asset_id,omitempty"`
}

func toCreativeSpec(r *creativeRequest) *integrations.CreativeSpec {
	if r == nil {
		return nil
	}
	return &integrations.CreativeSpec{
		PrimaryText:  r.PrimaryText,
		Headline:     r.Headline,
		Description:  r.Description,
		LinkURL:      r.LinkURL,
		ImageURL:     r.ImageURL,
		VideoURL:     r.VideoURL,
		Format:       r.Format,
		MediaAssetID: r.MediaAssetID,
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
		Name:             req.Name,
		Objective:        req.Objective,
		DailyBudget:      req.DailyBudget,
		Currency:         req.Currency,
		StartDate:        start,
		EndDate:          end,
		CTA:              req.CTA,
		AdAccountIDs:     req.AdAccountIDs,
		Platforms:        req.Platforms,
		BidStrategy:      req.BidStrategy,
		BidCap:           req.BidCap,
		PacingType:       req.PacingType,
		FrequencyCap:     req.FrequencyCap,
		FrequencyCapUnit: req.FrequencyCapUnit,
		Targeting:        req.Targeting,
		Creative:         toCreativeSpec(req.Creative),
		Variants:         toVariantSpecs(req.Variants),
		Provenance:       req.Provenance,
		Rationale:        req.Rationale,
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
	if camp.BidStrategy != "" {
		m["bid_strategy"] = camp.BidStrategy
	}
	if camp.BidCap.Valid {
		if f, ok := numericToFloat(camp.BidCap); ok {
			m["bid_cap"] = f
		}
	}
	if camp.PacingType != "" {
		m["pacing_type"] = camp.PacingType
	}
	if camp.FrequencyCap.Valid {
		m["frequency_cap"] = camp.FrequencyCap.Int32
	}
	if camp.FrequencyCapTimeUnit.Valid {
		m["frequency_cap_time_unit"] = camp.FrequencyCapTimeUnit.String
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
	if len(camp.Provenance) > 0 {
		m["provenance"] = json.RawMessage(camp.Provenance)
	}
	if len(camp.Rationale) > 0 {
		m["rationale"] = json.RawMessage(camp.Rationale)
	}
	if len(camp.Variants) > 0 {
		m["variants"] = json.RawMessage(camp.Variants)
	} else {
		if len(camp.Targeting) > 0 {
			m["targeting"] = json.RawMessage(camp.Targeting)
		}
		if len(camp.Creative) > 0 {
			m["creative"] = json.RawMessage(camp.Creative)
		}
	}
	return m
}

func toVariantSpecs(reqs []variantRequest) []integrations.VariantSpec {
	if reqs == nil {
		return nil
	}
	out := make([]integrations.VariantSpec, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, integrations.VariantSpec{
			Targeting: r.Targeting,
			Creative:  toCreativeSpec(r.Creative),
		})
	}
	return out
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

// Pause sets a live campaign to PAUSED.
func (h *CampaignHandler) Pause(c echo.Context) error {
	return h.setStatus(c, service.StatusPaused)
}

// Resume sets a paused campaign back to ACTIVE.
func (h *CampaignHandler) Resume(c echo.Context) error {
	return h.setStatus(c, service.StatusActive)
}

func (h *CampaignHandler) setStatus(c echo.Context, status string) error {
	ws, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	cid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid campaign id")
	}
	camp, platform, err := h.svc.SetCampaignStatus(c.Request().Context(), ws, cid, status)
	if err != nil {
		return manageError(c, err)
	}
	return c.JSON(http.StatusOK, campaignToMap(camp, platform))
}

type updateBudgetRequest struct {
	DailyBudget *float64          `json:"daily_budget"`
	Provenance  map[string]string `json:"provenance,omitempty"`
}

// Forecast returns estimated reach/spend for an unsaved targeting spec.
func (h *CampaignHandler) Forecast(c echo.Context) error {
	workspaceID, _ := c.Get("workspace_id").(string)
	wid, err := uuid.Parse(workspaceID)
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}

	var req integrations.ForecastSpec
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	if strings.TrimSpace(req.Platform) == "" {
		return badRequest(c, "platform is required")
	}
	adAccountID := c.QueryParam("ad_account_id")
	if adAccountID == "" {
		return badRequest(c, "ad_account_id query param is required")
	}
	req.AdAccountID = adAccountID

	result, err := h.svc.ForecastDelivery(c.Request().Context(), wid, req.AdAccountID, req)
	if err != nil {
		var ve service.ValidationError
		if errors.As(err, &ve) {
			return badRequest(c, ve.Msg)
		}
		slog.Error("forecast delivery failed", "error", err)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to estimate delivery"})
	}
	return c.JSON(http.StatusOK, result)
}

// UpdateBudget changes a campaign's daily budget.
func (h *CampaignHandler) UpdateBudget(c echo.Context) error {
	ws, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	cid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid campaign id")
	}
	var req updateBudgetRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	if req.DailyBudget == nil {
		return badRequest(c, "daily_budget is required")
	}
	camp, platform, err := h.svc.UpdateCampaignBudget(c.Request().Context(), ws, cid, *req.DailyBudget)
	if err != nil {
		return manageError(c, err)
	}
	if len(req.Provenance) > 0 {
		camp, _ = h.svc.UpdateProvenance(c.Request().Context(), ws, cid, req.Provenance)
	}
	return c.JSON(http.StatusOK, campaignToMap(camp, platform))
}

// Health returns Oma's campaign health card (advice). The recommended fix, if
// any, is described so the UI can offer a one-click "Apply" (POST .../health/apply).
func (h *CampaignHandler) Health(c echo.Context) error {
	ws, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	cid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid campaign id")
	}

	assessment, _, err := h.svc.AssessHealth(c.Request().Context(), ws, cid)
	if err != nil {
		if errors.Is(err, service.ErrCampaignNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "campaign not found"})
		}
		slog.Error("health assessment failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to assess campaign health"})
	}

	resp := map[string]interface{}{
		"status":         assessment.Status,
		"what":           assessment.What,
		"why":            assessment.Why,
		"recommendation": assessment.Recommendation,
	}
	if assessment.Action != nil {
		ra := map[string]interface{}{
			"type":    assessment.Action.Type,
			"summary": assessment.Action.Summary,
		}
		if assessment.Action.Status != "" {
			ra["status"] = assessment.Action.Status
		}
		if assessment.Action.DailyBudget != 0 {
			ra["daily_budget"] = assessment.Action.DailyBudget
		}
		resp["recommended_action"] = ra
	}
	return c.JSON(http.StatusOK, resp)
}

// ApplyHealth executes the current recommended fix in one click: it re-derives
// the recommendation, records it as an Oma action, and runs it through the
// shared engine (so it's audited like any other action).
func (h *CampaignHandler) ApplyHealth(c echo.Context) error {
	ws, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	cid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid campaign id")
	}
	ctx := c.Request().Context()

	assessment, _, err := h.svc.AssessHealth(ctx, ws, cid)
	if err != nil {
		if errors.Is(err, service.ErrCampaignNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "campaign not found"})
		}
		slog.Error("health assessment failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to assess campaign health"})
	}
	if assessment.Action == nil {
		return badRequest(c, "there is no recommended action to apply")
	}

	var action db.CampaignAction
	switch assessment.Action.Type {
	case service.ActionSetStatus:
		action, err = h.actions.ProposeStatusChange(ctx, ws, service.ActorOma, cid, assessment.Action.Status)
	case service.ActionUpdateBudget:
		action, err = h.actions.ProposeBudgetChange(ctx, ws, service.ActorOma, cid, assessment.Action.DailyBudget)
	default:
		return badRequest(c, "unsupported recommended action")
	}
	if err != nil {
		var ve service.ValidationError
		if errors.As(err, &ve) {
			return badRequest(c, ve.Msg)
		}
		slog.Error("propose health action failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to apply recommendation"})
	}

	executed, _, err := h.actions.ApproveAction(ctx, ws, uuid.UUID(action.ID.Bytes))
	if err != nil {
		slog.Error("apply health action failed", "error", err)
		return c.JSON(http.StatusBadGateway, map[string]interface{}{
			"error":  "failed to apply recommendation",
			"action": actionToMap(executed),
		})
	}

	// Mark provenance: Oma applied this change.
	if executed.Status == service.ActionStatusExecuted {
		h.svc.UpdateProvenance(ctx, ws, cid, map[string]string{
			assessmentActionField(assessment): string(integrations.ProvenanceOma),
		})
	}

	return c.JSON(http.StatusOK, actionToMap(executed))
}

func assessmentActionField(a service.HealthAssessment) string {
	if a.Action == nil {
		return ""
	}
	switch a.Action.Type {
	case service.ActionSetStatus:
		return "status"
	case service.ActionUpdateBudget:
		return "daily_budget"
	}
	return ""
}

func manageError(c echo.Context, err error) error {
	var ve service.ValidationError
	switch {
	case errors.Is(err, service.ErrCampaignNotFound):
		return c.JSON(http.StatusNotFound, map[string]string{"error": "campaign not found"})
	case errors.As(err, &ve):
		return badRequest(c, ve.Msg)
	default:
		slog.Error("campaign management failed", "error", err)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to apply the change on the platform"})
	}
}
