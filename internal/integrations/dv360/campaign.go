package dv360

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

// DV360's hierarchy is campaign -> insertion order -> line item. Pill4rs stores
// the mapping in the existing deliverable fields:
//
//	CampaignID  -> DV360 campaignId
//	AdID        -> DV360 insertionOrderId
//	AdSetID     -> DV360 lineItemId (budget/bid/targeting live here)
//	CreativeID  -> DV360 creativeId (phase 2)
//
// EnsureDeliverable is idempotent: any object whose ID is already present in
// `have` is skipped, so a failed launch can resume where it stopped.

// EnsureDeliverable builds (or resumes) the paused DV360 tree.
func (c *Client) EnsureDeliverable(ctx context.Context, refreshToken string, account integrations.PlatformAccount, spec integrations.CampaignSpec, have integrations.DeliverableState) (integrations.DeliverableState, error) {
	state := have
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return state, err
	}

	advertiserID := account.AccountID
	if advertiserID == "" {
		return state, fmt.Errorf("dv360 advertiser id is required")
	}

	start, end := normalizeFlight(spec)
	days := daysInclusive(start, end)
	dailyMicros := micros(spec.DailyBudget)
	totalMicros := dailyMicros * int64(days)

	if state.CampaignID == "" {
		campaignID, err := c.createCampaign(ctx, at, advertiserID, spec, start, end, totalMicros)
		if err != nil {
			return state, err
		}
		state.CampaignID = campaignID
	}

	if state.AdID == "" {
		ioID, err := c.createInsertionOrder(ctx, at, advertiserID, state.CampaignID, spec, start, end, totalMicros)
		if err != nil {
			return state, err
		}
		state.AdID = ioID
	}

	createdCreative := false
	if state.CreativeID == "" {
		creativeID, err := c.createCreative(ctx, at, advertiserID, spec)
		if err != nil {
			return state, err
		}
		state.CreativeID = creativeID
		createdCreative = true
	}

	if state.AdSetID == "" {
		lineItemID, err := c.createLineItem(ctx, at, advertiserID, state.AdID, state.CreativeID, spec, start, end, totalMicros, dailyMicros)
		if err != nil {
			return state, err
		}
		state.AdSetID = lineItemID
	} else if createdCreative {
		if err := c.attachCreative(ctx, at, advertiserID, state.AdSetID, state.CreativeID); err != nil {
			return state, err
		}
	}

	if err := c.ensureTargeting(ctx, at, advertiserID, state.AdSetID, spec.FirstVariantTargeting()); err != nil {
		return state, err
	}

	return state, nil
}

func (c *Client) createCampaign(ctx context.Context, accessToken, advertiserID string, spec integrations.CampaignSpec, start, end time.Time, totalMicros int64) (string, error) {
	body := map[string]any{
		"displayName":  spec.Name,
		"entityStatus": "ENTITY_STATUS_PAUSED",
		"campaignGoal": map[string]any{
			"campaignGoalType": campaignGoalType(spec.Objective),
			"performanceGoal": map[string]any{
				"performanceGoalType":         performanceGoalType(spec.Objective),
				"performanceGoalAmountMicros": strconv.FormatInt(goalAmountMicros(spec), 10),
			},
		},
		"campaignFlight": map[string]any{
			"plannedDates":             dateRange(start, end),
			"plannedSpendAmountMicros": strconv.FormatInt(totalMicros, 10),
		},
		"frequencyCap": frequencyCap(spec),
	}

	var out struct {
		CampaignID string `json:"campaignId"`
	}
	path := fmt.Sprintf("/advertisers/%s/campaigns", url.PathEscape(advertiserID))
	if err := c.postJSON(ctx, accessToken, c.apiURL(path), body, &out); err != nil {
		return "", fmt.Errorf("create dv360 campaign: %w", err)
	}
	if out.CampaignID == "" {
		return "", fmt.Errorf("dv360 campaign create returned no campaignId")
	}
	return out.CampaignID, nil
}

func (c *Client) createInsertionOrder(ctx context.Context, accessToken, advertiserID, campaignID string, spec integrations.CampaignSpec, start, end time.Time, totalMicros int64) (string, error) {
	body := map[string]any{
		"displayName":           spec.Name + " - IO",
		"entityStatus":          "ENTITY_STATUS_DRAFT",
		"campaignId":            campaignID,
		"optimizationObjective": optimizationObjective(spec.Objective),
		"kpi": map[string]any{
			"kpiType":         kpiType(spec.Objective),
			"kpiAmountMicros": strconv.FormatInt(goalAmountMicros(spec), 10),
		},
		"frequencyCap": frequencyCap(spec),
		"budget": map[string]any{
			"budgetUnit": "BUDGET_UNIT_CURRENCY",
			"budgetSegments": []any{map[string]any{
				"dateRange":          dateRange(start, end),
				"budgetAmountMicros": strconv.FormatInt(totalMicros, 10),
			}},
		},
		"pacing": map[string]any{
			"pacingType":   "PACING_TYPE_EVEN",
			"pacingPeriod": "PACING_PERIOD_FLIGHT",
		},
	}

	var out struct {
		InsertionOrderID string `json:"insertionOrderId"`
	}
	path := fmt.Sprintf("/advertisers/%s/insertionOrders", url.PathEscape(advertiserID))
	if err := c.postJSON(ctx, accessToken, c.apiURL(path), body, &out); err != nil {
		return "", fmt.Errorf("create dv360 insertion order: %w", err)
	}
	if out.InsertionOrderID == "" {
		return "", fmt.Errorf("dv360 insertion order create returned no insertionOrderId")
	}
	if err := c.patchInsertionOrderStatus(ctx, accessToken, advertiserID, out.InsertionOrderID, "ENTITY_STATUS_PAUSED"); err != nil {
		return out.InsertionOrderID, err
	}
	return out.InsertionOrderID, nil
}

func (c *Client) createLineItem(ctx context.Context, accessToken, advertiserID, insertionOrderID, creativeID string, spec integrations.CampaignSpec, start, end time.Time, totalMicros, dailyMicros int64) (string, error) {
	body := map[string]any{
		"displayName":      spec.Name + " - line item",
		"entityStatus":     "ENTITY_STATUS_DRAFT",
		"insertionOrderId": insertionOrderID,
		"lineItemType":     lineItemType(spec.Format),
		"flight": map[string]any{
			"flightDateType": "LINE_ITEM_FLIGHT_DATE_TYPE_CUSTOM",
			"dateRange":      dateRange(start, end),
		},
		"budget": map[string]any{
			"budgetAllocationType": "LINE_ITEM_BUDGET_ALLOCATION_TYPE_FIXED",
			"maxAmount":            strconv.FormatInt(totalMicros, 10),
		},
		"pacing": map[string]any{
			"pacingType":     "PACING_TYPE_EVEN",
			"pacingPeriod":   "PACING_PERIOD_DAILY",
			"dailyMaxMicros": strconv.FormatInt(dailyMicros, 10),
		},
		"frequencyCap": frequencyCap(spec),
		"partnerRevenueModel": map[string]any{
			"markupType":   "PARTNER_REVENUE_MODEL_MARKUP_TYPE_CPM",
			"markupAmount": "0",
		},
		"bidStrategy": bidStrategy(spec),
	}
	if creativeID != "" {
		body["creativeIds"] = []string{creativeID}
	}

	var out struct {
		LineItemID string `json:"lineItemId"`
	}
	path := fmt.Sprintf("/advertisers/%s/lineItems", url.PathEscape(advertiserID))
	if err := c.postJSON(ctx, accessToken, c.apiURL(path), body, &out); err != nil {
		return "", fmt.Errorf("create dv360 line item: %w", err)
	}
	if out.LineItemID == "" {
		return "", fmt.Errorf("dv360 line item create returned no lineItemId")
	}
	if err := c.patchLineItemStatus(ctx, accessToken, advertiserID, out.LineItemID, "ENTITY_STATUS_PAUSED"); err != nil {
		return out.LineItemID, err
	}
	return out.LineItemID, nil
}

// SetStatus flips the campaign and all of its descendants (insertion orders and
// line items) between ACTIVE and PAUSED. DV360 requires the line item itself to
// be enabled for delivery.
func (c *Client) SetStatus(ctx context.Context, refreshToken, advertiserID, campaignID, status string) error {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return err
	}
	entityStatus := entityStatus(status)
	if entityStatus == "" {
		return fmt.Errorf("unsupported campaign status %q", status)
	}

	if err := c.patchCampaignStatus(ctx, at, advertiserID, campaignID, entityStatus); err != nil {
		return err
	}

	ios, err := c.listInsertionOrders(ctx, at, advertiserID, campaignID)
	if err != nil {
		return err
	}
	for _, io := range ios {
		if err := c.patchInsertionOrderStatus(ctx, at, advertiserID, io, entityStatus); err != nil {
			return err
		}
		items, err := c.listLineItems(ctx, at, advertiserID, io)
		if err != nil {
			return err
		}
		for _, li := range items {
			if err := c.patchLineItemStatus(ctx, at, advertiserID, li, entityStatus); err != nil {
				return err
			}
		}
	}
	return nil
}

// UpdateAdSetBudget updates the line item's fixed budget and daily pacing. In
// Pill4rs, the "ad set" for DV360 is the line item (the budget-bearing object).
func (c *Client) UpdateAdSetBudget(ctx context.Context, refreshToken, advertiserID, lineItemID string, dailyBudget float64) error {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return err
	}

	var li struct {
		Flight struct {
			DateRange struct {
				StartDate dateJSON `json:"startDate"`
				EndDate   dateJSON `json:"endDate"`
			} `json:"dateRange"`
		} `json:"flight"`
	}
	path := fmt.Sprintf("/advertisers/%s/lineItems/%s", url.PathEscape(advertiserID), url.PathEscape(lineItemID))
	if err := c.getJSON(ctx, at, c.apiURL(path), &li); err != nil {
		return fmt.Errorf("load dv360 line item: %w", err)
	}

	start := li.Flight.DateRange.StartDate.time()
	end := li.Flight.DateRange.EndDate.time()
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return fmt.Errorf("dv360 line item has no usable flight dates")
	}
	days := daysInclusive(start, end)
	totalMicros := micros(dailyBudget) * int64(days)

	body := map[string]any{
		"budget": map[string]any{
			"budgetAllocationType": "LINE_ITEM_BUDGET_ALLOCATION_TYPE_FIXED",
			"maxAmount":            strconv.FormatInt(totalMicros, 10),
		},
		"pacing": map[string]any{
			"pacingType":     "PACING_TYPE_EVEN",
			"pacingPeriod":   "PACING_PERIOD_DAILY",
			"dailyMaxMicros": strconv.FormatInt(micros(dailyBudget), 10),
		},
	}
	patchURL := c.apiURL(path) + "?updateMask=budget,pacing"
	if err := c.patchJSON(ctx, at, patchURL, body, nil); err != nil {
		return fmt.Errorf("update dv360 line item budget: %w", err)
	}
	return nil
}

func (c *Client) patchCampaignStatus(ctx context.Context, accessToken, advertiserID, campaignID, status string) error {
	path := fmt.Sprintf("/advertisers/%s/campaigns/%s?updateMask=entityStatus",
		url.PathEscape(advertiserID), url.PathEscape(campaignID))
	if err := c.patchJSON(ctx, accessToken, c.apiURL(path), map[string]any{"entityStatus": status}, nil); err != nil {
		return fmt.Errorf("set dv360 campaign status: %w", err)
	}
	return nil
}

func (c *Client) patchInsertionOrderStatus(ctx context.Context, accessToken, advertiserID, insertionOrderID, status string) error {
	path := fmt.Sprintf("/advertisers/%s/insertionOrders/%s?updateMask=entityStatus",
		url.PathEscape(advertiserID), url.PathEscape(insertionOrderID))
	if err := c.patchJSON(ctx, accessToken, c.apiURL(path), map[string]any{"entityStatus": status}, nil); err != nil {
		return fmt.Errorf("set dv360 insertion order status: %w", err)
	}
	return nil
}

func (c *Client) patchLineItemStatus(ctx context.Context, accessToken, advertiserID, lineItemID, status string) error {
	path := fmt.Sprintf("/advertisers/%s/lineItems/%s?updateMask=entityStatus",
		url.PathEscape(advertiserID), url.PathEscape(lineItemID))
	if err := c.patchJSON(ctx, accessToken, c.apiURL(path), map[string]any{"entityStatus": status}, nil); err != nil {
		return fmt.Errorf("set dv360 line item status: %w", err)
	}
	return nil
}

func (c *Client) listInsertionOrders(ctx context.Context, accessToken, advertiserID, campaignID string) ([]string, error) {
	q := url.Values{}
	q.Set("filter", fmt.Sprintf(`campaignId="%s"`, campaignID))
	path := fmt.Sprintf("/advertisers/%s/insertionOrders?%s", url.PathEscape(advertiserID), q.Encode())
	items, err := c.listAll(ctx, accessToken, c.apiURL(path), "insertionOrders")
	if err != nil {
		return nil, fmt.Errorf("list dv360 insertion orders: %w", err)
	}
	rows, err := decodeAll[struct {
		InsertionOrderID string `json:"insertionOrderId"`
	}](items)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.InsertionOrderID != "" {
			out = append(out, r.InsertionOrderID)
		}
	}
	return out, nil
}

func (c *Client) listLineItems(ctx context.Context, accessToken, advertiserID, insertionOrderID string) ([]string, error) {
	q := url.Values{}
	q.Set("filter", fmt.Sprintf(`insertionOrderId="%s"`, insertionOrderID))
	path := fmt.Sprintf("/advertisers/%s/lineItems?%s", url.PathEscape(advertiserID), q.Encode())
	items, err := c.listAll(ctx, accessToken, c.apiURL(path), "lineItems")
	if err != nil {
		return nil, fmt.Errorf("list dv360 line items: %w", err)
	}
	rows, err := decodeAll[struct {
		LineItemID string `json:"lineItemId"`
	}](items)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.LineItemID != "" {
			out = append(out, r.LineItemID)
		}
	}
	return out, nil
}

func normalizeFlight(spec integrations.CampaignSpec) (time.Time, time.Time) {
	today := dateOnly(time.Now().UTC())
	start := dateOnly(spec.StartDate)
	if start.IsZero() || !start.After(today) {
		start = today.AddDate(0, 0, 1)
	}
	end := dateOnly(spec.EndDate)
	if end.IsZero() || end.Before(start) {
		end = start.AddDate(0, 0, 30)
	}
	return start, end
}

func dateOnly(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func daysInclusive(start, end time.Time) int {
	d := int(end.Sub(start).Hours()/24) + 1
	if d < 1 {
		return 1
	}
	return d
}

func dateRange(start, end time.Time) map[string]any {
	return map[string]any{
		"startDate": dateObj(start),
		"endDate":   dateObj(end),
	}
}

func dateObj(t time.Time) map[string]int {
	y, m, d := t.Date()
	return map[string]int{"year": y, "month": int(m), "day": d}
}

type dateJSON struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

func (d dateJSON) time() time.Time {
	if d.Year == 0 || d.Month == 0 || d.Day == 0 {
		return time.Time{}
	}
	return time.Date(d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, time.UTC)
}

func micros(amount float64) int64 {
	if amount <= 0 {
		return 0
	}
	return int64(math.Round(amount * 1_000_000))
}

func entityStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "ACTIVE":
		return "ENTITY_STATUS_ACTIVE"
	case "PAUSED":
		return "ENTITY_STATUS_PAUSED"
	case "DRAFT":
		return "ENTITY_STATUS_DRAFT"
	default:
		return ""
	}
}

func frequencyCap(spec integrations.CampaignSpec) map[string]any {
	if spec.FrequencyCap <= 0 {
		return map[string]any{"unlimited": true}
	}
	unit := "TIME_UNIT_DAYS"
	switch strings.ToLower(strings.TrimSpace(spec.FrequencyCapUnit)) {
	case integrations.FreqCapHour:
		unit = "TIME_UNIT_HOURS"
	case integrations.FreqCapWeek:
		unit = "TIME_UNIT_WEEKS"
	}
	return map[string]any{
		"maxImpressions": spec.FrequencyCap,
		"timeUnit":       unit,
		"timeUnitCount":  1,
	}
}

func campaignGoalType(obj integrations.CampaignObjective) string {
	switch obj {
	case integrations.ObjectiveAwareness:
		return "CAMPAIGN_GOAL_TYPE_BRAND_AWARENESS"
	case integrations.ObjectiveAppPromotion:
		return "CAMPAIGN_GOAL_TYPE_APP_INSTALL"
	default:
		return "CAMPAIGN_GOAL_TYPE_ONLINE_ACTION"
	}
}

func performanceGoalType(obj integrations.CampaignObjective) string {
	switch obj {
	case integrations.ObjectiveAwareness:
		return "PERFORMANCE_GOAL_TYPE_CPM"
	case integrations.ObjectiveTraffic, integrations.ObjectiveEngagement:
		return "PERFORMANCE_GOAL_TYPE_CPC"
	default:
		return "PERFORMANCE_GOAL_TYPE_CPA"
	}
}

func optimizationObjective(obj integrations.CampaignObjective) string {
	switch obj {
	case integrations.ObjectiveAwareness:
		return "BRAND_AWARENESS"
	case integrations.ObjectiveTraffic, integrations.ObjectiveEngagement:
		return "CLICK"
	default:
		return "CONVERSION"
	}
}

func kpiType(obj integrations.CampaignObjective) string {
	switch obj {
	case integrations.ObjectiveAwareness:
		return "KPI_TYPE_CPM"
	case integrations.ObjectiveTraffic, integrations.ObjectiveEngagement:
		return "KPI_TYPE_CPC"
	default:
		return "KPI_TYPE_CPA"
	}
}

func biddingGoalType(obj integrations.CampaignObjective) string {
	switch obj {
	case integrations.ObjectiveLeads, integrations.ObjectiveSales, integrations.ObjectiveAppPromotion:
		return "BIDDING_STRATEGY_PERFORMANCE_GOAL_TYPE_CPA"
	default:
		return "BIDDING_STRATEGY_PERFORMANCE_GOAL_TYPE_CPC"
	}
}

func goalAmountMicros(spec integrations.CampaignSpec) int64 {
	if spec.BidCap > 0 {
		return micros(spec.BidCap)
	}
	switch performanceGoalType(spec.Objective) {
	case "PERFORMANCE_GOAL_TYPE_CPM":
		return micros(1.0)
	case "PERFORMANCE_GOAL_TYPE_CPC":
		return micros(0.10)
	default:
		return micros(1.0)
	}
}

func bidStrategy(spec integrations.CampaignSpec) map[string]any {
	// Custom bidding algorithms are created in DV360 (script or rule based) and
	// referenced by ID; Pill4rs can assign an existing algorithm to the line item.
	if algo := stringValue(spec.FirstVariantTargeting()["custom_bidding_algorithm_id"]); algo != "" {
		return map[string]any{
			"maximizeSpendAutoBid": map[string]any{
				"performanceGoalType":      "BIDDING_STRATEGY_PERFORMANCE_GOAL_TYPE_CUSTOM_ALGO",
				"customBiddingAlgorithmId": algo,
			},
		}
	}

	goal := biddingGoalType(spec.Objective)
	if spec.BidCap > 0 || spec.BidStrategy == integrations.BidStrategyCostCap || spec.BidStrategy == integrations.BidStrategyRoasGoal {
		return map[string]any{
			"performanceGoalAutoBid": map[string]any{
				"performanceGoalType":         goal,
				"performanceGoalAmountMicros": strconv.FormatInt(goalAmountMicros(spec), 10),
			},
		}
	}
	maxCPM := goalAmountMicros(spec)
	if maxCPM < micros(1.0) {
		maxCPM = micros(1.0)
	}
	return map[string]any{
		"maximizeSpendAutoBid": map[string]any{
			"performanceGoalType":          goal,
			"maxAverageCpmBidAmountMicros": strconv.FormatInt(maxCPM, 10),
		},
	}
}

func lineItemType(format string) string {
	if strings.EqualFold(strings.TrimSpace(format), "video") {
		return "LINE_ITEM_TYPE_VIDEO_DEFAULT"
	}
	return "LINE_ITEM_TYPE_DISPLAY_DEFAULT"
}
