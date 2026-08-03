package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

var _ integrations.CampaignCreator = (*Client)(nil)

// metaObjectiveMap translates internal objectives to Meta's ODAX outcome objectives.
var metaObjectiveMap = map[integrations.CampaignObjective]string{
	integrations.ObjectiveAwareness:    "OUTCOME_AWARENESS",
	integrations.ObjectiveTraffic:      "OUTCOME_TRAFFIC",
	integrations.ObjectiveEngagement:   "OUTCOME_ENGAGEMENT",
	integrations.ObjectiveLeads:        "OUTCOME_LEADS",
	integrations.ObjectiveSales:        "OUTCOME_SALES",
	integrations.ObjectiveAppPromotion: "OUTCOME_APP_PROMOTION",
}

// metaOptimization maps an objective to ad-set optimization_goal, billing_event,
// and (for conversion objectives) the pixel custom_event_type.
func metaOptimization(o integrations.CampaignObjective) (optimizationGoal, billingEvent, conversionEvent string) {
	switch o {
	case integrations.ObjectiveAwareness:
		return "REACH", "IMPRESSIONS", ""
	case integrations.ObjectiveTraffic:
		return "LINK_CLICKS", "IMPRESSIONS", ""
	case integrations.ObjectiveEngagement:
		return "POST_ENGAGEMENT", "IMPRESSIONS", ""
	case integrations.ObjectiveLeads:
		return "OFFSITE_CONVERSIONS", "IMPRESSIONS", "LEAD"
	case integrations.ObjectiveSales:
		return "OFFSITE_CONVERSIONS", "IMPRESSIONS", "PURCHASE"
	default:
		return "LINK_CLICKS", "IMPRESSIONS", ""
	}
}

// Platform identifies this adapter.
func (c *Client) Platform() string { return "meta" }

// EnsureDeliverable builds (or resumes) the full Meta delivery tree, all PAUSED.
func (c *Client) EnsureDeliverable(ctx context.Context, accessToken string, account integrations.PlatformAccount, spec integrations.CampaignSpec, have integrations.DeliverableState) (integrations.DeliverableState, error) {
	state := have

	if state.CampaignID == "" {
		cc, err := c.CreateCampaign(ctx, accessToken, account.AccountID, spec)
		if err != nil {
			return state, err
		}
		state.CampaignID = cc.ExternalID
	}
	if state.AdSetID == "" {
		id, err := c.createAdSet(ctx, accessToken, account, state.CampaignID, spec)
		if err != nil {
			return state, err
		}
		state.AdSetID = id
	}
	if state.CreativeID == "" {
		id, err := c.createCreative(ctx, accessToken, account, spec)
		if err != nil {
			return state, err
		}
		state.CreativeID = id
	}
	if state.AdID == "" {
		id, err := c.createAd(ctx, accessToken, account, spec, state.AdSetID, state.CreativeID)
		if err != nil {
			return state, err
		}
		state.AdID = id
	}
	return state, nil
}

// CreateCampaign creates just the campaign object (PAUSED). Used as step 1 of
// EnsureDeliverable. Targeting/CTA/dates/creative belong to lower tiers.
func (c *Client) CreateCampaign(ctx context.Context, accessToken, accountID string, spec integrations.CampaignSpec) (*integrations.CreatedCampaign, error) {
	objective, ok := metaObjectiveMap[spec.Objective]
	if !ok {
		return nil, fmt.Errorf("objective %q is not supported by meta", spec.Objective)
	}

	form := url.Values{}
	form.Set("name", spec.Name)
	form.Set("objective", objective)
	form.Set("status", "PAUSED")
	form.Set("special_ad_categories", "[]")
	if spec.DailyBudget > 0 {
		form.Set("daily_budget", minorUnits(spec.DailyBudget))
	}
	form.Set("access_token", accessToken)

	endpoint := fmt.Sprintf("%s/%s/%s/campaigns", c.graphBaseURL, c.apiVersion, ensureActPrefix(accountID))
	id, err := c.postForm(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}
	return &integrations.CreatedCampaign{ExternalID: id, Status: "PAUSED"}, nil
}

func (c *Client) createAdSet(ctx context.Context, accessToken string, account integrations.PlatformAccount, campaignID string, spec integrations.CampaignSpec) (string, error) {
	optGoal, billing, event := metaOptimization(spec.Objective)
	targeting := spec.FirstVariantTargeting()

	form := url.Values{}
	form.Set("name", spec.Name+" — ad set")
	form.Set("campaign_id", campaignID)
	if spec.DailyBudget > 0 {
		form.Set("daily_budget", minorUnits(spec.DailyBudget))
	}
	form.Set("billing_event", billing)
	form.Set("optimization_goal", optGoal)
	form.Set("bid_strategy", bidStrategy(spec))
	form.Set("status", "PAUSED")
	form.Set("targeting", metaTargeting(targeting))
	if !spec.StartDate.IsZero() {
		form.Set("start_time", spec.StartDate.UTC().Format(time.RFC3339))
	}
	if !spec.EndDate.IsZero() {
		form.Set("end_time", spec.EndDate.UTC().Format(time.RFC3339))
	}
	if event != "" && account.PixelID != "" {
		po, _ := json.Marshal(map[string]string{"pixel_id": account.PixelID, "custom_event_type": event})
		form.Set("promoted_object", string(po))
	}
	if spec.PacingType != "" && spec.PacingType != integrations.PacingStandard {
		form.Set("pacing_type", strings.ToUpper(spec.PacingType)[0:1]+strings.ToUpper(spec.PacingType)[1:])
	}
	if spec.FrequencyCap > 0 && spec.FrequencyCapUnit != "" {
		form.Set("frequency_cap", strconv.Itoa(spec.FrequencyCap))
		form.Set("frequency_cap_time_unit", spec.FrequencyCapUnit)
	}
	form.Set("access_token", accessToken)

	endpoint := fmt.Sprintf("%s/%s/%s/adsets", c.graphBaseURL, c.apiVersion, ensureActPrefix(account.AccountID))
	return c.postForm(ctx, endpoint, form)
}

func (c *Client) createCreative(ctx context.Context, accessToken string, account integrations.PlatformAccount, spec integrations.CampaignSpec) (string, error) {
	cr := spec.FirstVariantCreative()
	if cr == nil {
		return "", fmt.Errorf("creative is required")
	}
	if account.PageID == "" {
		return "", fmt.Errorf("a facebook page is required to create an ad creative")
	}

	linkData := map[string]any{"link": cr.LinkURL}
	if cr.PrimaryText != "" {
		linkData["message"] = cr.PrimaryText
	}
	if cr.Headline != "" {
		linkData["name"] = cr.Headline
	}
	if cr.Description != "" {
		linkData["description"] = cr.Description
	}
	if cr.ImageURL != "" {
		linkData["picture"] = cr.ImageURL
	}
	if spec.CTA != "" {
		linkData["call_to_action"] = map[string]any{
			"type":  spec.CTA,
			"value": map[string]any{"link": cr.LinkURL},
		}
	}
	storySpec, _ := json.Marshal(map[string]any{"page_id": account.PageID, "link_data": linkData})

	form := url.Values{}
	form.Set("name", spec.Name+" — creative")
	form.Set("object_story_spec", string(storySpec))
	form.Set("access_token", accessToken)

	endpoint := fmt.Sprintf("%s/%s/%s/adcreatives", c.graphBaseURL, c.apiVersion, ensureActPrefix(account.AccountID))
	return c.postForm(ctx, endpoint, form)
}

func (c *Client) createAd(ctx context.Context, accessToken string, account integrations.PlatformAccount, spec integrations.CampaignSpec, adSetID, creativeID string) (string, error) {
	creative, _ := json.Marshal(map[string]string{"creative_id": creativeID})

	form := url.Values{}
	form.Set("name", spec.Name+" — ad")
	form.Set("adset_id", adSetID)
	form.Set("creative", string(creative))
	form.Set("status", "PAUSED")
	form.Set("access_token", accessToken)

	endpoint := fmt.Sprintf("%s/%s/%s/ads", c.graphBaseURL, c.apiVersion, ensureActPrefix(account.AccountID))
	return c.postForm(ctx, endpoint, form)
}

// postForm POSTs form-encoded params to a Graph edge that returns {"id": "..."}.
func (c *Client) postForm(ctx context.Context, endpoint string, form url.Values) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var parsed struct {
		ID    string      `json:"id"`
		Error *graphError `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if parsed.Error != nil {
		return "", parsed.Error
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request failed: status %d: %s", resp.StatusCode, string(body))
	}
	if parsed.ID == "" {
		return "", fmt.Errorf("response missing id")
	}
	return parsed.ID, nil
}

func minorUnits(f float64) string {
	return strconv.FormatInt(int64(math.Round(f*100)), 10)
}

func bidStrategy(spec integrations.CampaignSpec) string {
	if spec.BidStrategy == "" {
		return "LOWEST_COST_WITHOUT_CAP"
	}
	return strings.ToUpper(spec.BidStrategy)
}

// EstimateDelivery calls Meta's delivery_estimate endpoint and returns reach,
// impressions, spend, CPM, and click estimates for an unsaved targeting spec.
// It satisfies integrations.CampaignForecaster.
var _ integrations.CampaignForecaster = (*Client)(nil)

func (c *Client) EstimateDelivery(ctx context.Context, accessToken string, account integrations.PlatformAccount, spec integrations.ForecastSpec) (*integrations.ForecastResult, error) {
	optGoal, _, _ := metaOptimization(integrations.CampaignObjective(spec.Objective))

	payload := map[string]any{
		"targeting_spec":    metaTargetingMap(spec.Targeting),
		"optimization_goal": optGoal,
		"access_token":      accessToken,
	}
	if spec.DailyBudget > 0 {
		payload["daily_budget"] = int64(math.Round(spec.DailyBudget * 100))
	}

	body, _ := json.Marshal(payload)
	form := url.Values{}
	form.Set("access_token", accessToken)
	for k, v := range payload {
		if k == "access_token" {
			continue
		}
		if vb, ok := v.(string); ok {
			form.Set(k, vb)
		} else if b, err := json.Marshal(v); err == nil {
			form.Set(k, string(b))
		}
	}
	_ = body

	endpoint := fmt.Sprintf("%s/%s/%s/delivery_estimate?%s", c.graphBaseURL, c.apiVersion, ensureActPrefix(account.AccountID), form.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build forecast request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("forecast request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read forecast response: %w", err)
	}

	var parsed struct {
		Data []struct {
			EstimateDAU int64          `json:"estimate_dau"`
			EstimateMau int64          `json:"estimate_mau"`
			BidEstimate map[string]any `json:"bid_estimate"`
			Error       *graphError    `json:"error"`
		} `json:"data"`
		Error *graphError `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode forecast response: %w", err)
	}
	if parsed.Error != nil {
		return nil, parsed.Error
	}
	if len(parsed.Data) == 0 {
		return c.fallbackForecast(spec)
	}

	d := parsed.Data[0]
	if d.Error != nil {
		return nil, d.Error
	}

	reach := d.EstimateMau
	if reach == 0 {
		reach = d.EstimateDAU * 30
	}

	result := &integrations.ForecastResult{
		EstimatedReach: reach,
		Currency:       spec.Currency,
	}
	if result.Currency == "" {
		result.Currency = "NGN"
	}

	if d.BidEstimate != nil {
		if r, ok := toInt64(d.BidEstimate["reach"]); ok && r > 0 {
			result.EstimatedReach = r
		}
		if imp, ok := toInt64(d.BidEstimate["impressions"]); ok {
			result.EstimatedImpressions = imp
		}
		if sp, ok := toFloat64(d.BidEstimate["spend"]); ok {
			result.EstimatedSpend = sp
		}
		if cpm, ok := toFloat64(d.BidEstimate["cpm"]); ok {
			result.EstimatedCPM = cpm
		}
		if cl, ok := toInt64(d.BidEstimate["clicks"]); ok {
			result.EstimatedClicks = cl
		}
	}
	if result.EstimatedImpressions == 0 && reach > 0 && spec.DailyBudget > 0 {
		result.EstimatedImpressions = reach
		result.EstimatedCPM = spec.DailyBudget / float64(reach) * 1000
		result.EstimatedSpend = spec.DailyBudget
	}
	if result.EstimatedCPM > 0 && result.EstimatedImpressions > 0 {
		result.EstimatedSpend = result.EstimatedCPM * float64(result.EstimatedImpressions) / 1000
	}
	if result.EstimatedImpressions > 0 && result.EstimatedCPM == 0 {
		result.EstimatedCPM = (spec.DailyBudget / float64(result.EstimatedImpressions)) * 1000
	}
	if result.EstimatedReach > 0 && result.EstimatedClicks == 0 {
		result.EstimatedClicks = int64(float64(result.EstimatedReach) * 0.01) // 1% CTR assumption
	}

	return result, nil
}

func (c *Client) fallbackForecast(spec integrations.ForecastSpec) (*integrations.ForecastResult, error) {
	avgCPM := 300.0
	impressions := int64((spec.DailyBudget / avgCPM) * 1000)
	reach := int64(float64(impressions) * 0.5)
	clicks := int64(float64(impressions) * 0.01)
	return &integrations.ForecastResult{
		EstimatedReach:       reach,
		EstimatedImpressions: impressions,
		EstimatedSpend:       spec.DailyBudget,
		EstimatedCPM:         avgCPM,
		EstimatedClicks:      clicks,
		Currency:             spec.Currency,
	}, nil
}

func metaTargetingMap(t map[string]any) map[string]any {
	if t == nil {
		t = map[string]any{}
	}
	if _, ok := t["geo_locations"]; ok {
		return t
	}
	if geo, ok := toStringSliceFromMap(t, "geo"); ok {
		t["geo_locations"] = map[string]any{"countries": geo}
	} else if countries, ok := toStringSliceFromMap(t, "countries"); ok {
		t["geo_locations"] = map[string]any{"countries": countries}
	} else {
		t["geo_locations"] = map[string]any{"countries": []string{"US"}}
	}
	return t
}

func toStringSliceFromMap(m map[string]any, key string) ([]string, bool) {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case []string:
			return x, true
		case []any:
			var out []string
			for _, e := range x {
				if s, ok := e.(string); ok {
					out = append(out, s)
				}
			}
			if len(out) > 0 {
				return out, true
			}
		}
	}
	return nil, false
}

func toInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int64:
		return x, true
	case int:
		return int64(x), true
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n, true
		}
		f, err := x.Float64()
		if err == nil {
			return int64(f), true
		}
	}
	return 0, false
}

func toFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

// metaTargeting builds a Meta targeting spec from the generic targeting map. If
// the caller already supplied a full Meta targeting object (has geo_locations),
// it is used verbatim.
func metaTargeting(t map[string]any) string {
	if t != nil {
		if _, ok := t["geo_locations"]; ok {
			if b, err := json.Marshal(t); err == nil {
				return string(b)
			}
		}
	}

	countries := []string{"US"}
	out := map[string]any{}
	if t != nil {
		if v, ok := firstStringSlice(t["geo"], t["countries"]); ok {
			countries = v
		}
		if v, ok := toInt(t["age_min"]); ok {
			out["age_min"] = v
		}
		if v, ok := toInt(t["age_max"]); ok {
			out["age_max"] = v
		}
		if g, ok := t["genders"]; ok {
			if codes := integrations.MetaGenderCodes(integrations.GenderValues(g)); len(codes) > 0 {
				out["genders"] = codes
			}
		}
	}
	out["geo_locations"] = map[string]any{"countries": countries}

	b, _ := json.Marshal(out)
	return string(b)
}

func firstStringSlice(vals ...any) ([]string, bool) {
	for _, v := range vals {
		if s, ok := toStringSlice(v); ok {
			return s, true
		}
	}
	return nil, false
}

func toStringSlice(v any) ([]string, bool) {
	switch x := v.(type) {
	case []string:
		if len(x) > 0 {
			return x, true
		}
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out, true
		}
	}
	return nil, false
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	}
	return 0, false
}
