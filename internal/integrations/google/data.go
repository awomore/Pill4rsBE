package google

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

var _ integrations.CampaignDataSource = (*Client)(nil)

// FetchCampaigns lists the customer's campaigns via GAQL.
func (c *Client) FetchCampaigns(ctx context.Context, accessToken, accountID string) ([]integrations.NormalizedCampaign, error) {
	at, err := c.accessToken(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	query := "SELECT campaign.id, campaign.name, campaign.status, campaign_budget.amount_micros FROM campaign"
	raw, err := c.search(ctx, at, accountID, query)
	if err != nil {
		return nil, err
	}
	var out struct {
		Results []struct {
			Campaign struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"campaign"`
			CampaignBudget struct {
				AmountMicros string `json:"amountMicros"`
			} `json:"campaignBudget"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode campaigns: %w", err)
	}
	res := make([]integrations.NormalizedCampaign, 0, len(out.Results))
	for _, r := range out.Results {
		res = append(res, integrations.NormalizedCampaign{
			ExternalID:  r.Campaign.ID,
			Name:        r.Campaign.Name,
			Objective:   "SEARCH",
			Status:      mapStatus(r.Campaign.Status),
			DailyBudget: microStringToMajor(r.CampaignBudget.AmountMicros),
		})
	}
	return res, nil
}

// FetchInsights returns a campaign's daily insights via GAQL.
func (c *Client) FetchInsights(ctx context.Context, accessToken, accountID, campaignID, since, until string) ([]integrations.NormalizedInsight, error) {
	at, err := c.accessToken(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(
		"SELECT segments.date, metrics.cost_micros, metrics.impressions, metrics.clicks, metrics.conversions, metrics.ctr, metrics.average_cpc, metrics.average_cpm "+
			"FROM campaign WHERE campaign.id = %s AND segments.date BETWEEN '%s' AND '%s'",
		campaignID, since, until,
	)
	raw, err := c.search(ctx, at, accountID, query)
	if err != nil {
		return nil, err
	}
	var out struct {
		Results []struct {
			Segments struct {
				Date string `json:"date"`
			} `json:"segments"`
			Metrics struct {
				CostMicros  string  `json:"costMicros"`
				Impressions string  `json:"impressions"`
				Clicks      string  `json:"clicks"`
				Conversions float64 `json:"conversions"`
				Ctr         float64 `json:"ctr"`
				AverageCpc  string  `json:"averageCpc"`
				AverageCpm  string  `json:"averageCpm"`
			} `json:"metrics"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode report: %w", err)
	}
	res := make([]integrations.NormalizedInsight, 0, len(out.Results))
	for _, r := range out.Results {
		date := parseDate(r.Segments.Date)
		if date.IsZero() {
			continue
		}
		res = append(res, integrations.NormalizedInsight{
			Date:        date,
			Spend:       microStringToMajor(r.Metrics.CostMicros),
			Impressions: parseInt(r.Metrics.Impressions),
			Clicks:      parseInt(r.Metrics.Clicks),
			Conversions: int64(r.Metrics.Conversions + 0.5),
			CPC:         microStringToMajor(r.Metrics.AverageCpc),
			CPM:         microStringToMajor(r.Metrics.AverageCpm),
			CTR:         r.Metrics.Ctr * 100, // Google ctr is a 0..1 ratio
			Currency:    "USD",
		})
	}
	return res, nil
}

func mapStatus(googleStatus string) string {
	if googleStatus == "ENABLED" {
		return "ACTIVE"
	}
	return "PAUSED"
}

func parseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseInt(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}
