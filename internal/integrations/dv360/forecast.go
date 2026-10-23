package dv360

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

var _ integrations.CampaignForecaster = (*Client)(nil)

type plannableLocation struct {
	PlannableLocationID string `json:"plannableLocationId"`
	RegionCode          string `json:"regionCode"`
	DisplayName         string `json:"displayName"`
	GeoRegionType       string `json:"geoRegionType"`
}

type productMetadata struct {
	PlannableProductCode string `json:"plannableProductCode"`
	DisplayName          string `json:"displayName"`
}

// EstimateDelivery generates a DV360 reach forecast for an unsaved targeting
// spec. Reach forecasting is a beta, allowlist-only DV360 feature; callers
// should surface the upstream error when the advertiser is not enabled.
func (c *Client) EstimateDelivery(ctx context.Context, refreshToken string, account integrations.PlatformAccount, spec integrations.ForecastSpec) (*integrations.ForecastResult, error) {
	at, err := c.AccessToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	advertiserID := account.AccountID
	if advertiserID == "" {
		return nil, fmt.Errorf("dv360 advertiser id is required")
	}
	if spec.DailyBudget <= 0 {
		return nil, fmt.Errorf("daily_budget must be positive")
	}

	start, end := forecastDates(spec.StartDate, spec.EndDate)
	days := daysInclusive(start, end)
	totalMicros := micros(spec.DailyBudget) * int64(days)

	locations, err := c.retrievePlannableLocations(ctx, at, advertiserID)
	if err != nil {
		return nil, err
	}
	locationID := choosePlannableLocation(locations, spec.Targeting)
	if locationID == "" {
		return nil, fmt.Errorf("no plannable dv360 location is available")
	}

	products, err := c.retrievePlannableProducts(ctx, at, advertiserID, locationID)
	if err != nil {
		return nil, err
	}
	productCode := choosePlannableProduct(products)
	if productCode == "" {
		return nil, fmt.Errorf("no plannable dv360 product is available for this location")
	}

	targeting := map[string]any{"plannableLocationIds": []string{locationID}}
	if age, ok := plannableAgeRange(spec.Targeting); ok {
		targeting["ageRange"] = age
	}
	if genders, ok := plannableGenders(spec.Targeting); ok {
		targeting["genders"] = genders
	}
	if devices, ok := plannableDevices(spec.Targeting); ok {
		targeting["devices"] = devices
	}

	body := map[string]any{
		"currencyCode": currencyOrUSD(spec.Currency),
		"campaignDuration": map[string]any{
			"durationDays": days,
			"dateRange":    dateRange(start, end),
		},
		"targeting": targeting,
		"plannedProducts": []any{map[string]any{
			"plannableProductCode": productCode,
			"budgetMicros":         strconv.FormatInt(totalMicros, 10),
		}},
	}

	var out struct {
		ReachCurve struct {
			ReachForecasts []reachForecastPoint `json:"reachForecasts"`
		} `json:"reachCurve"`
	}
	path := fmt.Sprintf("/advertisers/%s/reachForecast:generateReachForecast", url.PathEscape(advertiserID))
	if err := c.postJSON(ctx, at, c.apiURL(path), body, &out); err != nil {
		return nil, fmt.Errorf("generate dv360 reach forecast: %w", err)
	}
	if len(out.ReachCurve.ReachForecasts) == 0 {
		return nil, fmt.Errorf("dv360 returned no reach forecast points")
	}

	point := closestForecastPoint(out.ReachCurve.ReachForecasts, totalMicros)
	spend := float64(parseMicros(point.CostMicros)) / 1_000_000
	if spend <= 0 {
		spend = float64(totalMicros) / 1_000_000
	}
	impressions := parseIntCell(point.Forecast.TotalImpressions)
	reach := parseIntCell(point.Forecast.TotalReach)
	cpm := 0.0
	if impressions > 0 {
		cpm = spend / float64(impressions) * 1000
	}

	return &integrations.ForecastResult{
		EstimatedReach:       reach,
		EstimatedImpressions: impressions,
		EstimatedSpend:       spend,
		EstimatedCPM:         cpm,
		Currency:             currencyOrUSD(spec.Currency),
	}, nil
}

func (c *Client) retrievePlannableLocations(ctx context.Context, accessToken, advertiserID string) ([]plannableLocation, error) {
	var out struct {
		PlannableLocations []plannableLocation `json:"plannableLocations"`
	}
	path := fmt.Sprintf("/advertisers/%s/reachForecast:retrievePlannableLocations", url.PathEscape(advertiserID))
	if err := c.getJSON(ctx, accessToken, c.apiURL(path), &out); err != nil {
		return nil, fmt.Errorf("retrieve dv360 plannable locations: %w", err)
	}
	return out.PlannableLocations, nil
}

func (c *Client) retrievePlannableProducts(ctx context.Context, accessToken, advertiserID, locationID string) ([]productMetadata, error) {
	var out struct {
		ProductMetadata []productMetadata `json:"productMetadata"`
	}
	q := url.Values{}
	q.Set("plannableLocationId", locationID)
	path := fmt.Sprintf("/advertisers/%s/reachForecast:retrievePlannableProducts?%s",
		url.PathEscape(advertiserID), q.Encode())
	if err := c.getJSON(ctx, accessToken, c.apiURL(path), &out); err != nil {
		return nil, fmt.Errorf("retrieve dv360 plannable products: %w", err)
	}
	return out.ProductMetadata, nil
}

func choosePlannableLocation(locations []plannableLocation, targeting map[string]any) string {
	if len(locations) == 0 {
		return ""
	}
	wanted, _ := stringSlice(targeting["geo"])
	if len(wanted) == 0 {
		wanted, _ = stringSlice(targeting["countries"])
	}
	for _, w := range wanted {
		for _, loc := range locations {
			if strings.EqualFold(loc.RegionCode, w) || strings.EqualFold(loc.DisplayName, w) {
				return loc.PlannableLocationID
			}
		}
	}
	for _, loc := range locations {
		if loc.GeoRegionType == "GEO_REGION_TYPE_COUNTRY" {
			return loc.PlannableLocationID
		}
	}
	return locations[0].PlannableLocationID
}

func choosePlannableProduct(products []productMetadata) string {
	for _, p := range products {
		if p.PlannableProductCode != "" {
			return p.PlannableProductCode
		}
	}
	return ""
}

func plannableAgeRange(targeting map[string]any) (string, bool) {
	minAge, ok := intValue(targeting["age_min"])
	if !ok {
		return "", false
	}
	maxAge := 65
	if v, ok := intValue(targeting["age_max"]); ok {
		maxAge = v
	}

	best := ""
	bestSpan := 1 << 30
	for _, c := range plannableAgeRanges {
		if c.min <= minAge && c.max >= maxAge {
			span := c.max - c.min
			if span < bestSpan {
				best = c.enum
				bestSpan = span
			}
		}
	}
	if best == "" {
		best = "PLANNABLE_AGE_RANGE_18_65_UP"
	}
	return best, true
}

func plannableGenders(targeting map[string]any) ([]string, bool) {
	g, ok := targeting["genders"]
	if !ok {
		return nil, false
	}
	out := make([]string, 0, 2)
	for _, gender := range integrations.GenderValues(g) {
		if gender == "female" {
			out = append(out, "GENDER_FEMALE")
		} else if gender == "male" {
			out = append(out, "GENDER_MALE")
		}
	}
	return out, len(out) > 0
}

func plannableDevices(targeting map[string]any) ([]string, bool) {
	input, ok := stringSlice(targeting["devices"])
	if !ok {
		input, ok = stringSlice(targeting["device"])
	}
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(input))
	for _, d := range input {
		if enum := deviceType(d); enum != "" {
			out = append(out, enum)
		}
	}
	return out, len(out) > 0
}

type reachForecastPoint struct {
	CostMicros string `json:"costMicros"`
	Forecast   struct {
		TotalReach       string `json:"totalReach"`
		TotalImpressions string `json:"totalImpressions"`
	} `json:"forecast"`
}

func closestForecastPoint(points []reachForecastPoint, targetMicros int64) reachForecastPoint {
	best := points[0]
	bestDiff := int64(1<<63 - 1)
	for _, p := range points {
		diff := parseMicros(p.CostMicros) - targetMicros
		if diff < 0 {
			diff = -diff
		}
		if diff < bestDiff {
			best = p
			bestDiff = diff
		}
	}
	return best
}

type plannableAgeRangeDef struct {
	min  int
	max  int
	enum string
}

var plannableAgeRanges = []plannableAgeRangeDef{
	{18, 24, "PLANNABLE_AGE_RANGE_18_24"},
	{18, 34, "PLANNABLE_AGE_RANGE_18_34"},
	{18, 44, "PLANNABLE_AGE_RANGE_18_44"},
	{18, 49, "PLANNABLE_AGE_RANGE_18_49"},
	{18, 54, "PLANNABLE_AGE_RANGE_18_54"},
	{18, 64, "PLANNABLE_AGE_RANGE_18_64"},
	{18, 200, "PLANNABLE_AGE_RANGE_18_65_UP"},
	{21, 34, "PLANNABLE_AGE_RANGE_21_34"},
	{21, 44, "PLANNABLE_AGE_RANGE_21_44"},
	{21, 49, "PLANNABLE_AGE_RANGE_21_49"},
	{21, 54, "PLANNABLE_AGE_RANGE_21_54"},
	{21, 64, "PLANNABLE_AGE_RANGE_21_64"},
	{21, 200, "PLANNABLE_AGE_RANGE_21_65_UP"},
	{25, 34, "PLANNABLE_AGE_RANGE_25_34"},
	{25, 44, "PLANNABLE_AGE_RANGE_25_44"},
	{25, 49, "PLANNABLE_AGE_RANGE_25_49"},
	{25, 54, "PLANNABLE_AGE_RANGE_25_54"},
	{25, 64, "PLANNABLE_AGE_RANGE_25_64"},
	{25, 200, "PLANNABLE_AGE_RANGE_25_65_UP"},
	{35, 44, "PLANNABLE_AGE_RANGE_35_44"},
	{35, 49, "PLANNABLE_AGE_RANGE_35_49"},
	{35, 54, "PLANNABLE_AGE_RANGE_35_54"},
	{35, 64, "PLANNABLE_AGE_RANGE_35_64"},
	{35, 200, "PLANNABLE_AGE_RANGE_35_65_UP"},
	{45, 54, "PLANNABLE_AGE_RANGE_45_54"},
	{45, 64, "PLANNABLE_AGE_RANGE_45_64"},
	{45, 200, "PLANNABLE_AGE_RANGE_45_65_UP"},
	{50, 200, "PLANNABLE_AGE_RANGE_50_65_UP"},
	{55, 64, "PLANNABLE_AGE_RANGE_55_64"},
	{55, 200, "PLANNABLE_AGE_RANGE_55_65_UP"},
	{65, 200, "PLANNABLE_AGE_RANGE_65_UP"},
}

func forecastDates(startStr, endStr string) (time.Time, time.Time) {
	today := dateOnly(time.Now().UTC())
	start := parseForecastDate(startStr)
	if start.IsZero() || !start.After(today) {
		start = today.AddDate(0, 0, 1)
	}
	end := parseForecastDate(endStr)
	if end.IsZero() || end.Before(start) {
		end = start.AddDate(0, 0, 30)
	}
	return start, end
}

func parseForecastDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return dateOnly(t)
		}
	}
	return time.Time{}
}

func currencyOrUSD(currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return "USD"
	}
	return currency
}
