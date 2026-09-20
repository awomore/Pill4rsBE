package zernio

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

// Adapter is a Zernio-backed integration for one ad network. It satisfies the
// same per-platform contracts as the native Meta/TikTok/Google adapters, so the
// sync, campaign engine and Oma's review work unchanged.
type Adapter struct {
	client   *Client
	platform string // Zernio ads platform key, e.g. linkedinads
	display  string // Zernio display platform, e.g. linkedin
}

var (
	_ integrations.CampaignAdapter    = (*Adapter)(nil)
	_ integrations.CampaignDataSource = (*Adapter)(nil)
)

// NewAdapters returns one adapter per Zernio ad network.
func NewAdapters(client *Client) []*Adapter {
	defs := []struct{ key, display string }{
		{PlatformMetaAds, "facebook"},
		{PlatformGoogleAds, "google"},
		{PlatformTikTokAds, "tiktok"},
		{PlatformLinkedInAds, "linkedin"},
		{PlatformPinterestAds, "pinterest"},
		{PlatformXAds, "twitter"},
		{PlatformOpenAIAds, "openai"},
	}
	out := make([]*Adapter, 0, len(defs))
	for _, d := range defs {
		out = append(out, &Adapter{client: client, platform: d.key, display: d.display})
	}
	return out
}

// Platform returns the Zernio ads platform key this adapter services.
func (a *Adapter) Platform() string { return a.platform }

// EnsureDeliverable creates a paused campaign + ad set + ad through Zernio.
func (a *Adapter) EnsureDeliverable(ctx context.Context, accessToken string, account integrations.PlatformAccount, spec integrations.CampaignSpec, have integrations.DeliverableState) (integrations.DeliverableState, error) {
	state := have
	if state.CampaignID != "" && state.AdID != "" {
		return state, nil
	}

	adAccountID, err := a.resolveAdAccount(ctx, account.AccountID)
	if err != nil {
		return state, err
	}

	cr := spec.FirstVariantCreative()
	if cr == nil {
		return state, fmt.Errorf("creative is required")
	}

	payload := map[string]any{
		"accountId":    account.AccountID,
		"adAccountId":  adAccountID,
		"name":         spec.Name,
		"goal":         zernioGoal(spec.Objective),
		"budgetAmount": spec.DailyBudget,
		"budgetType":   "daily",
		"status":       "PAUSED",
		"linkUrl":      cr.LinkURL,
	}
	if cr.Headline != "" {
		payload["headline"] = cr.Headline
	}
	if cr.PrimaryText != "" {
		payload["body"] = cr.PrimaryText
	}
	if cr.Description != "" {
		payload["description"] = cr.Description
	}
	if spec.CTA != "" {
		payload["callToAction"] = spec.CTA
	}
	if cr.VideoURL != "" {
		payload["video"] = map[string]any{"url": cr.VideoURL}
	} else if cr.ImageURL != "" {
		payload["imageUrl"] = cr.ImageURL
	}
	applyTargeting(payload, spec.FirstVariantTargeting())

	res, err := a.client.CreateAd(ctx, payload)
	if err != nil {
		return state, err
	}
	state.CampaignID = res.PlatformCampaignID
	state.AdSetID = res.PlatformAdSetID
	state.AdID = res.AdID
	return state, nil
}

// SetStatus pauses or activates a campaign.
func (a *Adapter) SetStatus(ctx context.Context, accessToken, accountID, externalCampaignID, status string) error {
	zstatus := "paused"
	if status == "ACTIVE" {
		zstatus = "active"
	}
	return a.client.SetCampaignStatus(ctx, externalCampaignID, a.display, zstatus)
}

// UpdateAdSetBudget sets the ad set's daily budget.
func (a *Adapter) UpdateAdSetBudget(ctx context.Context, accessToken, accountID, externalAdSetID string, dailyBudget float64) error {
	if strings.TrimSpace(externalAdSetID) == "" {
		return fmt.Errorf("ad set id is required to update the budget")
	}
	return a.client.UpdateAdSetBudget(ctx, externalAdSetID, a.display, dailyBudget, "daily")
}

// FetchCampaigns lists campaigns and their rolled-up metrics.
func (a *Adapter) FetchCampaigns(ctx context.Context, accessToken, accountID string) ([]integrations.NormalizedCampaign, error) {
	campaigns, err := a.client.ListCampaigns(ctx, accountID, "", "", "")
	if err != nil {
		return nil, err
	}
	out := make([]integrations.NormalizedCampaign, 0, len(campaigns))
	for _, c := range campaigns {
		out = append(out, integrations.NormalizedCampaign{
			ExternalID:  c.PlatformCampaignID,
			Name:        c.Name,
			Objective:   c.Objective,
			Status:      normalizeStatus(c.Status),
			DailyBudget: budgetAmount(c.Budget),
		})
	}
	return out, nil
}

// FetchInsights returns the campaign's daily metrics.
func (a *Adapter) FetchInsights(ctx context.Context, accessToken, accountID, campaignID, since, until string) ([]integrations.NormalizedInsight, error) {
	_, daily, err := a.client.CampaignAnalytics(ctx, campaignID, a.display, since, until)
	if err != nil {
		return nil, err
	}
	out := make([]integrations.NormalizedInsight, 0, len(daily))
	for _, d := range daily {
		date, perr := time.Parse("2006-01-02", strings.TrimSpace(d.Date))
		if perr != nil {
			continue
		}
		out = append(out, integrations.NormalizedInsight{
			Date:        date,
			Spend:       d.Spend,
			Impressions: d.Impressions,
			Clicks:      d.Clicks,
			Conversions: d.Conversions,
			Reach:       d.Reach,
			CPM:         d.CPM,
			CPC:         d.CPC,
			CTR:         d.CTR,
			ROAS:        d.Roas,
		})
	}
	return out, nil
}

func (a *Adapter) resolveAdAccount(ctx context.Context, accountID string) (string, error) {
	accounts, err := a.client.ListAdAccounts(ctx, accountID)
	if err != nil {
		return "", err
	}
	if len(accounts) == 0 {
		return "", fmt.Errorf("no ad account is available on this zernio account")
	}
	return accounts[0].ID, nil
}

func zernioGoal(o integrations.CampaignObjective) string {
	switch o {
	case integrations.ObjectiveAwareness:
		return "awareness"
	case integrations.ObjectiveTraffic:
		return "traffic"
	case integrations.ObjectiveEngagement:
		return "engagement"
	case integrations.ObjectiveLeads:
		return "lead_generation"
	case integrations.ObjectiveSales:
		return "conversions"
	case integrations.ObjectiveAppPromotion:
		return "app_promotion"
	default:
		return "traffic"
	}
}

func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active", "enabled":
		return "ACTIVE"
	default:
		return "PAUSED"
	}
}

func budgetAmount(b *Budget) float64 {
	if b == nil {
		return 0
	}
	return b.Amount
}

func applyTargeting(payload map[string]any, targeting map[string]any) {
	if len(targeting) == 0 {
		return
	}
	if geo, ok := stringSlice(targeting["geo"]); ok && len(geo) > 0 {
		payload["countries"] = geo
	}
	if v, ok := intValue(targeting["age_min"]); ok {
		payload["ageMin"] = v
	}
	if v, ok := intValue(targeting["age_max"]); ok {
		payload["ageMax"] = v
	}
}

func stringSlice(v any) ([]string, bool) {
	switch t := v.(type) {
	case []string:
		return t, len(t) > 0
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out, len(out) > 0
	}
	return nil, false
}

func intValue(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case json.Number:
		n, err := t.Int64()
		return int(n), err == nil
	}
	return 0, false
}
