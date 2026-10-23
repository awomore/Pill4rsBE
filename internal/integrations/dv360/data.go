package dv360

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

var _ integrations.CampaignDataSource = (*Client)(nil)

type campaignRow struct {
	CampaignID   string `json:"campaignId"`
	DisplayName  string `json:"displayName"`
	EntityStatus string `json:"entityStatus"`
	CampaignGoal struct {
		CampaignGoalType string `json:"campaignGoalType"`
	} `json:"campaignGoal"`
	CampaignFlight struct {
		PlannedDates struct {
			StartDate dateJSON `json:"startDate"`
			EndDate   dateJSON `json:"endDate"`
		} `json:"plannedDates"`
		PlannedSpendAmountMicros string `json:"plannedSpendAmountMicros"`
	} `json:"campaignFlight"`
}

type lineItemRow struct {
	LineItemID       string `json:"lineItemId"`
	InsertionOrderID string `json:"insertionOrderId"`
	Budget           struct {
		MaxAmount string `json:"maxAmount"`
	} `json:"budget"`
	Flight struct {
		DateRange struct {
			StartDate dateJSON `json:"startDate"`
			EndDate   dateJSON `json:"endDate"`
		} `json:"dateRange"`
	} `json:"flight"`
}

// FetchCampaigns lists an advertiser's campaigns and derives a daily budget
// from the line items attached to each campaign (falling back to the campaign's
// planned spend).
func (c *Client) FetchCampaigns(ctx context.Context, refreshToken, advertiserID string) ([]integrations.NormalizedCampaign, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}

	path := fmt.Sprintf("/advertisers/%s/campaigns", url.PathEscape(advertiserID))
	items, err := c.listAll(ctx, at, c.apiURL(path), "campaigns")
	if err != nil {
		return nil, err
	}
	rows, err := decodeAll[campaignRow](items)
	if err != nil {
		return nil, fmt.Errorf("decode dv360 campaigns: %w", err)
	}

	out := make([]integrations.NormalizedCampaign, 0, len(rows))
	for _, row := range rows {
		start := row.CampaignFlight.PlannedDates.StartDate.time()
		end := row.CampaignFlight.PlannedDates.EndDate.time()
		budget, lineItemID, insertionOrderID := c.campaignDailyBudget(ctx, at, advertiserID, row.CampaignID, start, end,
			parseMicros(row.CampaignFlight.PlannedSpendAmountMicros))

		out = append(out, integrations.NormalizedCampaign{
			ExternalID:  row.CampaignID,
			Name:        row.DisplayName,
			Objective:   mapCampaignGoal(row.CampaignGoal.CampaignGoalType),
			Status:      mapEntityStatus(row.EntityStatus),
			DailyBudget: budget,
			AdSetID:     lineItemID,
			AdID:        insertionOrderID,
		})
	}
	return out, nil
}

func (c *Client) campaignDailyBudget(ctx context.Context, accessToken, advertiserID, campaignID string, start, end time.Time, plannedSpendMicros int64) (float64, string, string) {
	days := daysInclusive(start, end)
	if start.IsZero() || end.IsZero() {
		days = 30
	}

	q := url.Values{}
	q.Set("filter", fmt.Sprintf(`campaignId="%s"`, campaignID))
	path := fmt.Sprintf("/advertisers/%s/lineItems?%s", url.PathEscape(advertiserID), q.Encode())
	items, err := c.listAll(ctx, accessToken, c.apiURL(path), "lineItems")
	if err == nil {
		rows, derr := decodeAll[lineItemRow](items)
		if derr == nil {
			var totalMicros int64
			lineItemID, insertionOrderID := "", ""
			for _, r := range rows {
				totalMicros += parseMicros(r.Budget.MaxAmount)
				if lineItemID == "" && r.LineItemID != "" {
					lineItemID = r.LineItemID
					insertionOrderID = r.InsertionOrderID
				}
			}
			if totalMicros > 0 {
				return float64(totalMicros) / 1_000_000 / float64(days), lineItemID, insertionOrderID
			}
		}
	}

	if plannedSpendMicros > 0 {
		return float64(plannedSpendMicros) / 1_000_000 / float64(days), "", ""
	}
	return 0, "", ""
}

// FetchInsights runs a Bid Manager query for one campaign and parses the CSV
// report into daily, platform-neutral insights.
func (c *Client) FetchInsights(ctx context.Context, refreshToken, advertiserID, campaignID, since, until string) ([]integrations.NormalizedInsight, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}

	start, err := parseReportDate(since)
	if err != nil {
		return nil, fmt.Errorf("invalid since date: %w", err)
	}
	end, err := parseReportDate(until)
	if err != nil {
		return nil, fmt.Errorf("invalid until date: %w", err)
	}

	query := map[string]any{
		"metadata": map[string]any{
			"title": "pill4rs-" + campaignID + "-" + strconv.FormatInt(time.Now().UnixNano(), 10),
			"dataRange": map[string]any{
				"range":           "CUSTOM_DATES",
				"customStartDate": dateObj(start),
				"customEndDate":   dateObj(end),
			},
			"format": "CSV",
		},
		"params": map[string]any{
			"type": "STANDARD",
			"metrics": []string{
				"METRIC_IMPRESSIONS",
				"METRIC_CLICKS",
				"METRIC_REVENUE_ADVERTISER",
				"METRIC_TOTAL_CONVERSIONS",
			},
			"filters": []any{
				map[string]string{"type": "FILTER_ADVERTISER", "value": advertiserID},
				map[string]string{"type": "FILTER_CAMPAIGN", "value": campaignID},
			},
			"groupBys": []string{"FILTER_DATE"},
		},
	}

	var created struct {
		QueryID string `json:"queryId"`
	}
	if err := c.postJSON(ctx, at, c.reportsURL("/queries"), query, &created); err != nil {
		return nil, fmt.Errorf("create bid manager query: %w", err)
	}
	if created.QueryID == "" {
		return nil, fmt.Errorf("bid manager query create returned no queryId")
	}

	var report struct {
		Key struct {
			ReportID string `json:"reportId"`
		} `json:"key"`
		Metadata struct {
			Status struct {
				State string `json:"state"`
			} `json:"status"`
		} `json:"metadata"`
	}
	runURL := c.reportsURL("/queries/" + url.PathEscape(created.QueryID) + ":run?synchronous=true")
	if err := c.postJSON(ctx, at, runURL, map[string]any{}, &report); err != nil {
		return nil, fmt.Errorf("run bid manager query: %w", err)
	}
	if report.Key.ReportID == "" {
		return nil, fmt.Errorf("bid manager run returned no reportId")
	}
	if state := report.Metadata.Status.State; state != "" && state != "DONE" {
		if err := c.waitForReport(ctx, at, created.QueryID, report.Key.ReportID); err != nil {
			return nil, err
		}
	}

	mediaURL := c.reportsURL("/queries/" + url.PathEscape(created.QueryID) +
		"/reports/" + url.PathEscape(report.Key.ReportID) + "?alt=media")
	raw, err := c.getRaw(ctx, c.reportsHTTPClient, mediaURL, at)
	if err != nil {
		// Some reports expose data only via a GCS object path. Retry there
		// before giving up (requires the devstorage.read_only scope).
		if gcsPath, metaErr := c.reportGCSURL(ctx, at, created.QueryID, report.Key.ReportID); metaErr == nil && gcsPath != "" {
			if gcsRaw, gcsErr := c.getRaw(ctx, c.reportsHTTPClient, gcsPath, at); gcsErr == nil {
				return parseReportCSV(gcsRaw)
			}
		}
		return nil, fmt.Errorf("download bid manager report: %w", err)
	}
	return parseReportCSV(raw)
}

// reportGCSURL returns the report's Google Cloud Storage URL, if any.
func (c *Client) reportGCSURL(ctx context.Context, accessToken, queryID, reportID string) (string, error) {
	var report struct {
		Metadata struct {
			GoogleCloudStoragePath string `json:"googleCloudStoragePath"`
		} `json:"metadata"`
	}
	reportURL := c.reportsURL("/queries/" + url.PathEscape(queryID) + "/reports/" + url.PathEscape(reportID))
	if err := c.getJSON(ctx, accessToken, reportURL, &report); err != nil {
		return "", err
	}
	path := strings.TrimSpace(report.Metadata.GoogleCloudStoragePath)
	if path == "" {
		return "", nil
	}
	if strings.HasPrefix(path, "gs://") {
		return c.gcsBaseURL + "/" + strings.TrimPrefix(path, "gs://"), nil
	}
	return path, nil
}

// parseReportCSV maps a Bid Manager CSV report into NormalizedInsight rows. It
// tolerates differing header labels (Bid Manager changes them over time).
func parseReportCSV(raw []byte) ([]integrations.NormalizedInsight, error) {
	reader := csv.NewReader(strings.NewReader(string(raw)))
	reader.FieldsPerRecord = -1
	headers, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, fmt.Errorf("read report header: %w", err)
	}

	idx := make(map[string]int, len(headers))
	for i, h := range headers {
		idx[normalizeHeader(h)] = i
	}

	var out []integrations.NormalizedInsight
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read report row: %w", err)
		}

		date := parseReportDateCell(cell(row, idx, "date"))
		if date.IsZero() {
			continue
		}
		spend := parseFloatCell(cell(row, idx, "revenueadvcurrency", "revenue", "spend"))
		impressions := parseIntCell(cell(row, idx, "impressions"))
		clicks := parseIntCell(cell(row, idx, "clicks"))
		conversions := parseIntCell(cell(row, idx, "totalconversions", "conversions"))

		ni := integrations.NormalizedInsight{
			Date:        date,
			Spend:       spend,
			Impressions: impressions,
			Clicks:      clicks,
			Conversions: conversions,
			CPM:         parseFloatCell(cell(row, idx, "cpm")),
			CPC:         parseFloatCell(cell(row, idx, "cpc")),
			CTR:         parseFloatCell(cell(row, idx, "ctr")),
		}
		if ni.CPM == 0 && impressions > 0 {
			ni.CPM = spend / float64(impressions) * 1000
		}
		if ni.CPC == 0 && clicks > 0 {
			ni.CPC = spend / float64(clicks)
		}
		if ni.CTR == 0 && impressions > 0 {
			ni.CTR = float64(clicks) / float64(impressions) * 100
		}
		out = append(out, ni)
	}
	return out, nil
}

func normalizeHeader(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	replacer := strings.NewReplacer(" ", "", "_", "", "-", "", "(", "", ")", "", ".", "")
	return replacer.Replace(h)
}

func cell(row []string, idx map[string]int, names ...string) string {
	for _, n := range names {
		if i, ok := idx[n]; ok && i < len(row) {
			return row[i]
		}
	}
	return ""
}

func parseFloatCell(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimPrefix(s, "$")
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func parseIntCell(s string) int64 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func parseMicros(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

func parseReportDate(s string) (time.Time, error) {
	return parseReportDateCellStrict(strings.TrimSpace(s))
}

func parseReportDateCell(s string) time.Time {
	t, _ := parseReportDateCellStrict(strings.TrimSpace(s))
	return t
}

func parseReportDateCellStrict(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	for _, layout := range []string{"2006-01-02", "2006/01/02", "01/02/2006", "1/2/2006", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return dateOnly(t), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date %q", s)
}

func mapEntityStatus(status string) string {
	switch status {
	case "ENTITY_STATUS_ACTIVE":
		return "ACTIVE"
	case "ENTITY_STATUS_DRAFT":
		return "PAUSED"
	default:
		return "PAUSED"
	}
}

func mapCampaignGoal(goal string) string {
	switch goal {
	case "CAMPAIGN_GOAL_TYPE_BRAND_AWARENESS":
		return string(integrations.ObjectiveAwareness)
	case "CAMPAIGN_GOAL_TYPE_APP_INSTALL":
		return string(integrations.ObjectiveAppPromotion)
	case "CAMPAIGN_GOAL_TYPE_OFFLINE_ACTION":
		return string(integrations.ObjectiveLeads)
	default:
		return string(integrations.ObjectiveTraffic)
	}
}

// waitForReport polls an async Bid Manager report until it reaches DONE. The
// synchronous run path normally finishes inline; this covers slow reports.
func (c *Client) waitForReport(ctx context.Context, accessToken, queryID, reportID string) error {
	reportURL := c.reportsURL("/queries/" + url.PathEscape(queryID) + "/reports/" + url.PathEscape(reportID))
	for attempt := 0; attempt < 15; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
		var report struct {
			Metadata struct {
				Status struct {
					State string `json:"state"`
				} `json:"status"`
			} `json:"metadata"`
		}
		if err := c.getJSON(ctx, accessToken, reportURL, &report); err != nil {
			return fmt.Errorf("poll bid manager report: %w", err)
		}
		switch report.Metadata.Status.State {
		case "DONE":
			return nil
		case "FAILED":
			return fmt.Errorf("bid manager report failed")
		}
	}
	return fmt.Errorf("bid manager report did not finish in time")
}
