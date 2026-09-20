package zernio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// AdAccount is a platform ad account reachable through a Zernio account.
type AdAccount struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Currency string `json:"currency"`
}

// ListAdAccounts lists the platform ad accounts behind a Zernio account.
func (c *Client) ListAdAccounts(ctx context.Context, accountID string) ([]AdAccount, error) {
	q := url.Values{}
	q.Set("accountId", accountID)
	raw, err := c.do(ctx, http.MethodGet, "/v1/ads/accounts", q, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Accounts []AdAccount `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode ad accounts: %w", err)
	}
	return out.Accounts, nil
}

// Budget is a campaign or ad-set budget in whole currency units.
type Budget struct {
	Amount float64 `json:"amount"`
	Type   string  `json:"type"` // daily | lifetime
}

// AdMetrics is Zernio's unified metric shape.
type AdMetrics struct {
	Spend         float64 `json:"spend"`
	Impressions   int64   `json:"impressions"`
	Reach         int64   `json:"reach"`
	Clicks        int64   `json:"clicks"`
	CTR           float64 `json:"ctr"`
	CPC           float64 `json:"cpc"`
	CPM           float64 `json:"cpm"`
	Conversions   int64   `json:"conversions"`
	Roas          float64 `json:"roas"`
	PurchaseValue float64 `json:"purchaseValue"`
}

// Campaign is one campaign with rolled-up metrics.
type Campaign struct {
	PlatformCampaignID string    `json:"platformCampaignId"`
	Platform           string    `json:"platform"`
	Name               string    `json:"campaignName"`
	Status             string    `json:"status"`
	Currency           string    `json:"currency"`
	Objective          string    `json:"platformObjective"`
	Budget             *Budget   `json:"budget"`
	Metrics            AdMetrics `json:"metrics"`
}

// ListCampaigns returns campaigns for a Zernio account (optionally scoped to a
// platform ad account and date window).
func (c *Client) ListCampaigns(ctx context.Context, accountID, adAccountID, fromDate, toDate string) ([]Campaign, error) {
	q := url.Values{}
	if accountID != "" {
		q.Set("accountId", accountID)
	}
	if adAccountID != "" {
		q.Set("adAccountId", adAccountID)
	}
	if fromDate != "" {
		q.Set("fromDate", fromDate)
	}
	if toDate != "" {
		q.Set("toDate", toDate)
	}
	q.Set("limit", "100")

	raw, err := c.do(ctx, http.MethodGet, "/v1/ads/campaigns", q, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Campaigns []Campaign `json:"campaigns"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode campaigns: %w", err)
	}
	return out.Campaigns, nil
}

// DailyMetric is one day of a campaign/ad analytics series.
type DailyMetric struct {
	Date string `json:"date"`
	AdMetrics
}

// CampaignAnalytics returns the summary and daily metrics for a campaign.
func (c *Client) CampaignAnalytics(ctx context.Context, campaignID, platform, fromDate, toDate string) (AdMetrics, []DailyMetric, error) {
	q := url.Values{}
	if platform != "" {
		q.Set("platform", platform)
	}
	if fromDate != "" {
		q.Set("fromDate", fromDate)
	}
	if toDate != "" {
		q.Set("toDate", toDate)
	}
	raw, err := c.do(ctx, http.MethodGet, "/v1/ads/campaigns/"+url.PathEscape(campaignID)+"/analytics", q, nil)
	if err != nil {
		return AdMetrics{}, nil, err
	}
	var out struct {
		Analytics struct {
			Summary AdMetrics     `json:"summary"`
			Daily   []DailyMetric `json:"daily"`
		} `json:"analytics"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return AdMetrics{}, nil, fmt.Errorf("decode analytics: %w", err)
	}
	return out.Analytics.Summary, out.Analytics.Daily, nil
}

// SetCampaignStatus pauses or activates a campaign. status is "active"/"paused".
func (c *Client) SetCampaignStatus(ctx context.Context, campaignID, platform, status string) error {
	body := map[string]any{"status": status}
	if platform != "" {
		body["platform"] = platform
	}
	_, err := c.do(ctx, http.MethodPut, "/v1/ads/campaigns/"+url.PathEscape(campaignID)+"/status", nil, body)
	return err
}

// UpdateCampaignBudget sets a campaign-level budget.
func (c *Client) UpdateCampaignBudget(ctx context.Context, campaignID, platform string, amount float64, budgetType string) error {
	body := map[string]any{"budget": Budget{Amount: amount, Type: budgetType}}
	if platform != "" {
		body["platform"] = platform
	}
	_, err := c.do(ctx, http.MethodPut, "/v1/ads/campaigns/"+url.PathEscape(campaignID), nil, body)
	return err
}

// UpdateAdSetBudget sets an ad-set-level budget.
func (c *Client) UpdateAdSetBudget(ctx context.Context, adSetID, platform string, amount float64, budgetType string) error {
	body := map[string]any{"budget": Budget{Amount: amount, Type: budgetType}}
	if platform != "" {
		body["platform"] = platform
	}
	_, err := c.do(ctx, http.MethodPut, "/v1/ads/ad-sets/"+url.PathEscape(adSetID), nil, body)
	return err
}

// Ad is the minimal ad object returned by create.
type Ad struct {
	ID                 string `json:"_id"`
	PlatformAdID       string `json:"platformAdId"`
	PlatformCampaignID string `json:"platformCampaignId"`
	PlatformAdSetID    string `json:"platformAdSetId"`
}

// CreateResult is the resolved delivery tree after creating an ad.
type CreateResult struct {
	PlatformCampaignID string
	PlatformAdSetID    string
	AdID               string
}

// CreateAd creates a campaign + ad set + ad in one Zernio call.
func (c *Client) CreateAd(ctx context.Context, payload map[string]any) (CreateResult, error) {
	raw, err := c.do(ctx, http.MethodPost, "/v1/ads/create", nil, payload)
	if err != nil {
		return CreateResult{}, err
	}
	var out struct {
		Ad                 *Ad    `json:"ad"`
		Ads                []Ad   `json:"ads"`
		PlatformCampaignID string `json:"platformCampaignId"`
		PlatformAdSetID    string `json:"platformAdSetId"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return CreateResult{}, fmt.Errorf("decode create: %w", err)
	}

	res := CreateResult{
		PlatformCampaignID: out.PlatformCampaignID,
		PlatformAdSetID:    out.PlatformAdSetID,
	}
	ad := out.Ad
	if ad == nil && len(out.Ads) > 0 {
		ad = &out.Ads[0]
	}
	if ad != nil {
		if res.PlatformCampaignID == "" {
			res.PlatformCampaignID = ad.PlatformCampaignID
		}
		if res.PlatformAdSetID == "" {
			res.PlatformAdSetID = ad.PlatformAdSetID
		}
		res.AdID = ad.ID
		if res.AdID == "" {
			res.AdID = ad.PlatformAdID
		}
	}
	if res.PlatformCampaignID == "" {
		return CreateResult{}, fmt.Errorf("zernio create returned no campaign id")
	}
	return res, nil
}
