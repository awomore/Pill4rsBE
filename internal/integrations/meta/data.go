package meta

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

const defaultCurrency = "NGN"

// Campaign is a raw campaign node from the Meta Graph API.
type Campaign struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Objective   string `json:"objective"`
	Status      string `json:"status"`
	DailyBudget string `json:"daily_budget"` // minor currency units (e.g. kobo/cents)
}

// InsightRow is a raw daily insight row from the Meta Graph API.
type InsightRow struct {
	DateStart       string          `json:"date_start"`
	DateStop        string          `json:"date_stop"`
	Spend           string          `json:"spend"`
	Impressions     string          `json:"impressions"`
	Clicks          string          `json:"clicks"`
	Reach           string          `json:"reach"`
	Cpm             string          `json:"cpm"`
	Cpc             string          `json:"cpc"`
	Ctr             string          `json:"ctr"`
	AccountCurrency string          `json:"account_currency"`
	Actions         []InsightAction `json:"actions"`
	PurchaseROAS    []InsightAction `json:"purchase_roas"`
}

// InsightAction is a single typed value within Meta's actions/purchase_roas arrays.
type InsightAction struct {
	ActionType string `json:"action_type"`
	Value      string `json:"value"`
}

type pagedResponse[T any] struct {
	Data   []T `json:"data"`
	Paging struct {
		Next string `json:"next"`
	} `json:"paging"`
	Error *graphError `json:"error"`
}

// GetCampaigns fetches all campaigns for the given ad account.
func (c *Client) GetCampaigns(ctx context.Context, accessToken, accountID string) ([]Campaign, error) {
	params := url.Values{}
	params.Set("fields", "id,name,objective,status,daily_budget")
	params.Set("limit", "200")
	params.Set("access_token", accessToken)
	endpoint := fmt.Sprintf("%s/%s/%s/campaigns?%s", c.graphBaseURL, c.apiVersion, ensureActPrefix(accountID), params.Encode())
	return fetchPaged[Campaign](ctx, c, endpoint)
}

// GetInsights fetches daily insight rows for a campaign over the [since, until]
// date range (inclusive, YYYY-MM-DD), one row per day.
func (c *Client) GetInsights(ctx context.Context, accessToken, campaignID, since, until string) ([]InsightRow, error) {
	params := url.Values{}
	params.Set("fields", "spend,impressions,clicks,reach,cpm,cpc,ctr,actions,purchase_roas,account_currency")
	params.Set("time_increment", "1")
	params.Set("time_range", fmt.Sprintf(`{"since":"%s","until":"%s"}`, since, until))
	params.Set("limit", "500")
	params.Set("access_token", accessToken)
	endpoint := fmt.Sprintf("%s/%s/%s/insights?%s", c.graphBaseURL, c.apiVersion, campaignID, params.Encode())
	return fetchPaged[InsightRow](ctx, c, endpoint)
}

func fetchPaged[T any](ctx context.Context, c *Client, endpoint string) ([]T, error) {
	var out []T
	next := endpoint
	for next != "" {
		var resp pagedResponse[T]
		if err := c.getJSON(ctx, next, &resp); err != nil {
			return nil, err
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		out = append(out, resp.Data...)
		next = resp.Paging.Next
	}
	return out, nil
}

// conversionActionTypes is the priority order used to pick a single conversion
// count from Meta's actions array (which can contain many overlapping types).
var conversionActionTypes = []string{
	"omni_purchase",
	"offsite_conversion.fb_pixel_purchase",
	"onsite_web_purchase",
	"purchase",
	"lead",
	"complete_registration",
}

// Normalize maps a raw Meta insight row into the common NormalizedInsight shape.
func Normalize(row InsightRow) integrations.NormalizedInsight {
	n := integrations.NormalizedInsight{
		Spend:       parseFloat(row.Spend),
		Impressions: parseInt(row.Impressions),
		Clicks:      parseInt(row.Clicks),
		Reach:       parseInt(row.Reach),
		CPM:         parseFloat(row.Cpm),
		CPC:         parseFloat(row.Cpc),
		CTR:         parseFloat(row.Ctr),
		Conversions: conversionsFromActions(row.Actions),
		ROAS:        firstActionValue(row.PurchaseROAS),
		Currency:    row.AccountCurrency,
	}
	if n.Currency == "" {
		n.Currency = defaultCurrency
	}
	if t, err := time.Parse("2006-01-02", row.DateStart); err == nil {
		n.Date = t
	}
	return n
}

func conversionsFromActions(actions []InsightAction) int64 {
	if len(actions) == 0 {
		return 0
	}
	byType := make(map[string]string, len(actions))
	for _, a := range actions {
		byType[a.ActionType] = a.Value
	}
	for _, t := range conversionActionTypes {
		if v, ok := byType[t]; ok {
			return int64(parseFloat(v) + 0.5)
		}
	}
	return 0
}

func firstActionValue(actions []InsightAction) float64 {
	if len(actions) == 0 {
		return 0
	}
	return parseFloat(actions[0].Value)
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}

func parseInt(s string) int64 {
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func ensureActPrefix(id string) string {
	if strings.HasPrefix(id, "act_") {
		return id
	}
	return "act_" + id
}

// Page is a Facebook Page the user manages (needed to build ad creatives).
type Page struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Pixel is a Meta ads pixel on the account (needed for conversion objectives).
type Pixel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// FetchPages lists the Facebook Pages the connected user manages.
func (c *Client) FetchPages(ctx context.Context, accessToken string) ([]Page, error) {
	params := url.Values{}
	params.Set("fields", "id,name")
	params.Set("limit", "100")
	params.Set("access_token", accessToken)
	endpoint := fmt.Sprintf("%s/%s/me/accounts?%s", c.graphBaseURL, c.apiVersion, params.Encode())
	return fetchPaged[Page](ctx, c, endpoint)
}

// FetchPixels lists the ads pixels on the given ad account.
func (c *Client) FetchPixels(ctx context.Context, accessToken, accountID string) ([]Pixel, error) {
	params := url.Values{}
	params.Set("fields", "id,name")
	params.Set("limit", "100")
	params.Set("access_token", accessToken)
	endpoint := fmt.Sprintf("%s/%s/%s/adspixels?%s", c.graphBaseURL, c.apiVersion, ensureActPrefix(accountID), params.Encode())
	return fetchPaged[Pixel](ctx, c, endpoint)
}

var _ integrations.CampaignDataSource = (*Client)(nil)

// FetchCampaigns returns the account's campaigns in the platform-agnostic shape.
func (c *Client) FetchCampaigns(ctx context.Context, accessToken, accountID string) ([]integrations.NormalizedCampaign, error) {
	raw, err := c.GetCampaigns(ctx, accessToken, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]integrations.NormalizedCampaign, 0, len(raw))
	for _, mc := range raw {
		out = append(out, integrations.NormalizedCampaign{
			ExternalID:  mc.ID,
			Name:        mc.Name,
			Objective:   mc.Objective,
			Status:      mc.Status,
			DailyBudget: minorStringToMajor(mc.DailyBudget),
		})
	}
	return out, nil
}

// FetchInsights returns a campaign's daily insights, already normalized.
func (c *Client) FetchInsights(ctx context.Context, accessToken, accountID, campaignID, since, until string) ([]integrations.NormalizedInsight, error) {
	rows, err := c.GetInsights(ctx, accessToken, campaignID, since, until)
	if err != nil {
		return nil, err
	}
	out := make([]integrations.NormalizedInsight, 0, len(rows))
	for _, r := range rows {
		out = append(out, Normalize(r))
	}
	return out, nil
}

type adAccountNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// FetchAdAccounts lists every ad account the connected user can access.
func (c *Client) FetchAdAccounts(ctx context.Context, accessToken string) ([]integrations.Account, error) {
	params := url.Values{}
	params.Set("fields", "id,name")
	params.Set("limit", "200")
	params.Set("access_token", accessToken)
	endpoint := fmt.Sprintf("%s/%s/me/adaccounts?%s", c.graphBaseURL, c.apiVersion, params.Encode())
	nodes, err := fetchPaged[adAccountNode](ctx, c, endpoint)
	if err != nil {
		return nil, err
	}
	out := make([]integrations.Account, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, integrations.Account{ID: n.ID, Name: n.Name})
	}
	return out, nil
}

// minorStringToMajor converts Meta's minor-unit integer string to major units.
func minorStringToMajor(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return float64(n) / 100
}
