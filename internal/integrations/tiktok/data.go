package tiktok

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

var _ integrations.CampaignDataSource = (*Client)(nil)

type campaignListData struct {
	List []struct {
		CampaignID      string  `json:"campaign_id"`
		CampaignName    string  `json:"campaign_name"`
		ObjectiveType   string  `json:"objective_type"`
		OperationStatus string  `json:"operation_status"`
		Budget          float64 `json:"budget"`
	} `json:"list"`
}

// FetchCampaigns lists the advertiser's campaigns in the common shape.
func (c *Client) FetchCampaigns(ctx context.Context, accessToken, accountID string) ([]integrations.NormalizedCampaign, error) {
	params := url.Values{}
	params.Set("advertiser_id", accountID)
	params.Set("page_size", "100")
	data, err := c.get(ctx, "/campaign/get/", accessToken, params)
	if err != nil {
		return nil, err
	}
	var cl campaignListData
	if err := json.Unmarshal(data, &cl); err != nil {
		return nil, fmt.Errorf("decode campaigns: %w", err)
	}
	out := make([]integrations.NormalizedCampaign, 0, len(cl.List))
	for _, c := range cl.List {
		out = append(out, integrations.NormalizedCampaign{
			ExternalID:  c.CampaignID,
			Name:        c.CampaignName,
			Objective:   c.ObjectiveType,
			Status:      mapStatus(c.OperationStatus),
			DailyBudget: c.Budget,
		})
	}
	return out, nil
}

type reportData struct {
	List []struct {
		Dimensions struct {
			CampaignID  string `json:"campaign_id"`
			StatTimeDay string `json:"stat_time_day"`
		} `json:"dimensions"`
		Metrics struct {
			Spend       string `json:"spend"`
			Impressions string `json:"impressions"`
			Clicks      string `json:"clicks"`
			Conversion  string `json:"conversion"`
			Reach       string `json:"reach"`
			Cpc         string `json:"cpc"`
			Cpm         string `json:"cpm"`
			Ctr         string `json:"ctr"`
		} `json:"metrics"`
	} `json:"list"`
}

// FetchInsights returns a campaign's daily insights, normalized. (TikTok's basic
// report has no revenue/ROAS, so ROAS is left at 0.)
func (c *Client) FetchInsights(ctx context.Context, accessToken, accountID, campaignID, since, until string) ([]integrations.NormalizedInsight, error) {
	params := url.Values{}
	params.Set("advertiser_id", accountID)
	params.Set("report_type", "BASIC")
	params.Set("data_level", "AUCTION_CAMPAIGN")
	params.Set("dimensions", `["campaign_id","stat_time_day"]`)
	params.Set("metrics", `["spend","impressions","clicks","conversion","reach","cpc","cpm","ctr"]`)
	params.Set("start_date", since)
	params.Set("end_date", until)
	params.Set("filtering", fmt.Sprintf(`{"campaign_ids":["%s"]}`, campaignID))
	params.Set("page_size", "1000")

	data, err := c.get(ctx, "/report/integrated/get/", accessToken, params)
	if err != nil {
		return nil, err
	}
	var rd reportData
	if err := json.Unmarshal(data, &rd); err != nil {
		return nil, fmt.Errorf("decode report: %w", err)
	}

	out := make([]integrations.NormalizedInsight, 0, len(rd.List))
	for _, row := range rd.List {
		date := parseDay(row.Dimensions.StatTimeDay)
		if date.IsZero() {
			continue
		}
		out = append(out, integrations.NormalizedInsight{
			Date:        date,
			Spend:       parseFloat(row.Metrics.Spend),
			Impressions: parseInt(row.Metrics.Impressions),
			Clicks:      parseInt(row.Metrics.Clicks),
			Conversions: parseInt(row.Metrics.Conversion),
			Reach:       parseInt(row.Metrics.Reach),
			CPC:         parseFloat(row.Metrics.Cpc),
			CPM:         parseFloat(row.Metrics.Cpm),
			CTR:         parseFloat(row.Metrics.Ctr),
			Currency:    "USD",
		})
	}
	return out, nil
}

func mapStatus(op string) string {
	if op == "ENABLE" {
		return "ACTIVE"
	}
	return "PAUSED"
}

func parseDay(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return time.Time{}
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func parseInt(s string) int64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return int64(f)
}
