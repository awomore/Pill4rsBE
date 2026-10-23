package dv360

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

// 1x1 transparent PNG.
var testPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")

func newTestClient(serverURL string) *Client {
	c := NewClient("client-id", "client-secret", "https://api.example.com/cb")
	c.oauthBaseURL = serverURL
	c.apiBaseURL = serverURL
	c.reportsBaseURL = serverURL
	c.gcsBaseURL = serverURL
	return c
}

func writeToken(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if r.Form.Get("grant_type") == "authorization_code" {
		_, _ = w.Write([]byte(`{"access_token":"at0","refresh_token":"rt","expires_in":3600}`))
		return
	}
	_, _ = w.Write([]byte(`{"access_token":"at","expires_in":3600}`))
}

func readBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode body %q: %v", string(raw), err)
	}
	return m
}

func TestGetOAuthURL(t *testing.T) {
	u, err := url.Parse(NewClient("cid", "sec", "https://x/cb").GetOAuthURL("st"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != "cid" || q.Get("state") != "st" || q.Get("access_type") != "offline" || q.Get("prompt") != "consent" {
		t.Errorf("query: %v", q)
	}
	scope := q.Get("scope")
	if !strings.Contains(scope, "display-video") || !strings.Contains(scope, "doubleclickbidmanager") {
		t.Errorf("scope: %q", scope)
	}
}

func TestExchangeCodeForToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(writeToken))
	defer srv.Close()

	tok, err := newTestClient(srv.URL).ExchangeCodeForToken(context.Background(), "code")
	if err != nil {
		t.Fatalf("ExchangeCodeForToken failed: %v", err)
	}
	if tok.AccessToken != "rt" {
		t.Errorf("expected refresh token, got %q", tok.AccessToken)
	}
}

func TestFetchAdvertisers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case r.URL.Path == "/v5/partners":
			_, _ = w.Write([]byte(`{"partners":[{"partnerId":"p1","displayName":"Partner"}]}`))
		case r.URL.Path == "/v5/advertisers":
			if got := r.URL.Query().Get("partnerId"); got != "p1" {
				t.Errorf("partnerId = %q", got)
			}
			_, _ = w.Write([]byte(`{"advertisers":[{"advertiserId":"a1","displayName":"Advertiser One"}]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	accounts, err := newTestClient(srv.URL).FetchAdvertisers(context.Background(), "rt")
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].ID != "a1" || accounts[0].Name != "Advertiser One" {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestEnsureDeliverable(t *testing.T) {
	start := time.Now().UTC().AddDate(0, 0, 1)
	end := start.AddDate(0, 0, 10)
	var campaignBody, ioBody, liBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/v5/advertisers/a1/campaigns":
			campaignBody = readBody(t, r)
			_, _ = w.Write([]byte(`{"campaignId":"c1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v5/advertisers/a1/insertionOrders":
			ioBody = readBody(t, r)
			_, _ = w.Write([]byte(`{"insertionOrderId":"io1"}`))
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/insertionOrders/io1"):
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/image.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(testPNG)
		case r.Method == http.MethodPost && r.URL.Path == "/upload/v5/advertisers/a1/assets":
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/related") {
				t.Errorf("asset upload content type = %q", r.Header.Get("Content-Type"))
			}
			_, _ = w.Write([]byte(`{"asset":{"mediaId":"asset1"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v5/advertisers/a1/creatives":
			_, _ = w.Write([]byte(`{"creativeId":"cr1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v5/advertisers/a1/lineItems":
			liBody = readBody(t, r)
			_, _ = w.Write([]byte(`{"lineItemId":"li1"}`))
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/lineItems/li1"):
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	spec := integrations.CampaignSpec{
		Name:             "Test campaign",
		Objective:        integrations.ObjectiveAwareness,
		DailyBudget:      10,
		StartDate:        start,
		EndDate:          end,
		FrequencyCap:     3,
		FrequencyCapUnit: integrations.FreqCapDay,
		Creative: &integrations.CreativeSpec{
			PrimaryText: "Hello",
			Headline:    "Test",
			LinkURL:     "https://example.com",
			ImageURL:    srv.URL + "/image.png",
			Format:      "image",
		},
	}
	state, err := newTestClient(srv.URL).EnsureDeliverable(context.Background(), "rt",
		integrations.PlatformAccount{AccountID: "a1"}, spec, integrations.DeliverableState{})
	if err != nil {
		t.Fatal(err)
	}
	if state.CampaignID != "c1" || state.AdID != "io1" || state.AdSetID != "li1" || state.CreativeID != "cr1" {
		t.Fatalf("state = %+v", state)
	}

	if campaignBody["entityStatus"] != "ENTITY_STATUS_PAUSED" {
		t.Errorf("campaign status = %v", campaignBody["entityStatus"])
	}
	goal, _ := campaignBody["campaignGoal"].(map[string]any)
	if goal["campaignGoalType"] != "CAMPAIGN_GOAL_TYPE_BRAND_AWARENESS" {
		t.Errorf("campaign goal = %v", goal)
	}

	// 11 inclusive days * $10 = $110 = 110,000,000 micros.
	budget, _ := ioBody["budget"].(map[string]any)
	segments, _ := budget["budgetSegments"].([]any)
	seg, _ := segments[0].(map[string]any)
	if seg["budgetAmountMicros"] != "110000000" {
		t.Errorf("IO budget = %v", seg["budgetAmountMicros"])
	}

	liBudget, _ := liBody["budget"].(map[string]any)
	if liBudget["maxAmount"] != "110000000" {
		t.Errorf("line item budget = %v", liBudget["maxAmount"])
	}
	if liBody["lineItemType"] != "LINE_ITEM_TYPE_DISPLAY_DEFAULT" {
		t.Errorf("line item type = %v", liBody["lineItemType"])
	}
	creativeIDs, _ := liBody["creativeIds"].([]any)
	if len(creativeIDs) != 1 || creativeIDs[0] != "cr1" {
		t.Errorf("line item creativeIds = %v", liBody["creativeIds"])
	}
}

func TestSetStatusUpdatesCampaignAndDescendants(t *testing.T) {
	var patched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case r.Method == http.MethodPatch:
			var body map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			patched = append(patched, r.URL.Path+":"+body["entityStatus"].(string))
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/v5/advertisers/a1/insertionOrders":
			_, _ = w.Write([]byte(`{"insertionOrders":[{"insertionOrderId":"io1"}]}`))
		case r.URL.Path == "/v5/advertisers/a1/lineItems":
			_, _ = w.Write([]byte(`{"lineItems":[{"lineItemId":"li1"}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	if err := newTestClient(srv.URL).SetStatus(context.Background(), "rt", "a1", "c1", "ACTIVE"); err != nil {
		t.Fatal(err)
	}
	if len(patched) != 3 {
		t.Fatalf("patched = %v", patched)
	}
	for _, p := range patched {
		if !strings.HasSuffix(p, ":ENTITY_STATUS_ACTIVE") {
			t.Errorf("unexpected patch %q", p)
		}
	}
}

func TestUpdateAdSetBudget(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/v5/advertisers/a1/lineItems/li1":
			_, _ = w.Write([]byte(`{"flight":{"dateRange":{"startDate":{"year":2026,"month":10,"day":1},"endDate":{"year":2026,"month":10,"day":10}}}}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/v5/advertisers/a1/lineItems/li1":
			got = readBody(t, r)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	if err := newTestClient(srv.URL).UpdateAdSetBudget(context.Background(), "rt", "a1", "li1", 5); err != nil {
		t.Fatal(err)
	}
	budget, _ := got["budget"].(map[string]any)
	if budget["maxAmount"] != "50000000" { // 10 days * $5
		t.Errorf("budget = %v", budget)
	}
	pacing, _ := got["pacing"].(map[string]any)
	if pacing["dailyMaxMicros"] != "5000000" {
		t.Errorf("pacing = %v", pacing)
	}
}

func TestFetchCampaigns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case r.URL.Path == "/v5/advertisers/a1/campaigns":
			_, _ = w.Write([]byte(`{"campaigns":[{"campaignId":"c1","displayName":"Camp","entityStatus":"ENTITY_STATUS_ACTIVE","campaignGoal":{"campaignGoalType":"CAMPAIGN_GOAL_TYPE_BRAND_AWARENESS"},"campaignFlight":{"plannedDates":{"startDate":{"year":2026,"month":10,"day":1},"endDate":{"year":2026,"month":10,"day":10}}}}]}`))
		case r.URL.Path == "/v5/advertisers/a1/lineItems":
			_, _ = w.Write([]byte(`{"lineItems":[{"lineItemId":"li1","insertionOrderId":"io1","budget":{"maxAmount":"50000000"},"flight":{"dateRange":{"startDate":{"year":2026,"month":10,"day":1},"endDate":{"year":2026,"month":10,"day":10}}}}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	camps, err := newTestClient(srv.URL).FetchCampaigns(context.Background(), "rt", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if len(camps) != 1 || camps[0].ExternalID != "c1" || camps[0].Status != "ACTIVE" {
		t.Fatalf("camps = %+v", camps)
	}
	if camps[0].DailyBudget != 5 { // 50 / 10 days
		t.Errorf("daily budget = %v", camps[0].DailyBudget)
	}
	if camps[0].AdSetID != "li1" || camps[0].AdID != "io1" {
		t.Errorf("imported ids = adset %q ad %q", camps[0].AdSetID, camps[0].AdID)
	}
}

func TestFetchInsightsParsesCSV(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/v2/queries":
			_, _ = w.Write([]byte(`{"queryId":"q1"}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, ":run"):
			_, _ = w.Write([]byte(`{"key":{"reportId":"r1"},"metadata":{"status":{"state":"DONE"}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/queries/q1/reports/r1":
			if r.URL.Query().Get("alt") != "media" {
				t.Errorf("expected alt=media, got %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte("Date,Impressions,Clicks,Revenue (Adv Currency),Total Conversions\n2026-10-01,1000,10,12.5,2\n"))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	insights, err := newTestClient(srv.URL).FetchInsights(context.Background(), "rt", "a1", "c1", "2026-10-01", "2026-10-02")
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %+v", insights)
	}
	ni := insights[0]
	if ni.Date.Format("2006-01-02") != "2026-10-01" || ni.Spend != 12.5 || ni.Impressions != 1000 || ni.Clicks != 10 || ni.Conversions != 2 {
		t.Errorf("insight = %+v", ni)
	}
	if ni.CPM != 12.5 || ni.CPC != 1.25 || ni.CTR != 1 {
		t.Errorf("derived metrics = cpm %v cpc %v ctr %v", ni.CPM, ni.CPC, ni.CTR)
	}
}

func TestEnsureTargetingIsIdempotent(t *testing.T) {
	var created []string
	geoCreated := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, ":search"):
			_, _ = w.Write([]byte(`{"targetingOptions":[{"targetingOptionId":"2840","geoRegionDetails":{"geoRegionType":"GEO_REGION_TYPE_COUNTRY","displayName":"United States"}}]}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/assignedTargetingOptions"):
			_, _ = w.Write([]byte(`{"assignedTargetingOptions":[]}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/assignedTargetingOptions"):
			created = append(created, r.URL.Path)
			if strings.Contains(r.URL.Path, targetingGeo) {
				body := readBody(t, r)
				geo, _ := body["geoRegionDetails"].(map[string]any)
				if geo["targetingOptionId"] != "2840" {
					t.Errorf("geo body = %v", body)
				}
				geoCreated = true
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	targeting := map[string]any{
		"geo":     []any{"US"},
		"age_min": float64(18),
		"age_max": float64(34),
		"genders": []any{"female"},
		"devices": []any{"mobile"},
		"dayparting": []any{
			map[string]any{"day": "monday", "start_hour": float64(9), "end_hour": float64(17)},
		},
	}
	c := newTestClient(srv.URL)
	if err := c.ensureTargeting(context.Background(), "at", "a1", "li1", targeting); err != nil {
		t.Fatal(err)
	}
	if !geoCreated {
		t.Error("geo targeting was not created")
	}
	// geo 1 + age 2 (18-24, 25-34) + gender 1 + device 1 + dayparting 1
	if len(created) != 6 {
		t.Errorf("created %d targeting options: %v", len(created), created)
	}
}

func TestEnsureTargetingAudiences(t *testing.T) {
	var created []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/assignedTargetingOptions"):
			_, _ = w.Write([]byte(`{"assignedTargetingOptions":[]}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/assignedTargetingOptions"):
			created = append(created, r.URL.Path)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	targeting := map[string]any{
		"custom_audiences":    []any{"fp1"},
		"google_audiences":    []any{map[string]any{"id": "g1", "type": "in_market"}},
		"combined_audiences":  []any{"c1"},
		"custom_lists":        []any{map[string]any{"id": "l1", "type": "custom_intent"}},
		"lookalike_audiences": []any{"lk1"},
	}
	c := newTestClient(srv.URL)
	if err := c.ensureTargeting(context.Background(), "at", "a1", "li1", targeting); err != nil {
		t.Fatal(err)
	}
	if len(created) != 5 {
		t.Fatalf("created %d audience options: %v", len(created), created)
	}
}

func TestBidStrategyCustomAlgorithm(t *testing.T) {
	spec := integrations.CampaignSpec{
		Objective: integrations.ObjectiveSales,
		Targeting: map[string]any{"custom_bidding_algorithm_id": "algo1"},
	}
	got := bidStrategy(spec)
	strategy, _ := got["maximizeSpendAutoBid"].(map[string]any)
	if strategy["customBiddingAlgorithmId"] != "algo1" {
		t.Errorf("strategy = %v", got)
	}
	if strategy["performanceGoalType"] != "BIDDING_STRATEGY_PERFORMANCE_GOAL_TYPE_CUSTOM_ALGO" {
		t.Errorf("goal type = %v", strategy["performanceGoalType"])
	}
}

func TestEstimateDelivery(t *testing.T) {
	start := time.Now().UTC().AddDate(0, 0, 1)
	end := start.AddDate(0, 0, 10)
	var forecastBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, ":retrievePlannableLocations"):
			_, _ = w.Write([]byte(`{"plannableLocations":[{"plannableLocationId":"loc1","regionCode":"US","geoRegionType":"GEO_REGION_TYPE_COUNTRY"}]}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, ":retrievePlannableProducts"):
			_, _ = w.Write([]byte(`{"productMetadata":[{"plannableProductCode":"DISPLAY_REACH"}]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, ":generateReachForecast"):
			forecastBody = readBody(t, r)
			_, _ = w.Write([]byte(`{"reachCurve":{"reachForecasts":[{"costMicros":"100000000","forecast":{"totalReach":"50000","totalImpressions":"100000"}},{"costMicros":"110000000","forecast":{"totalReach":"55000","totalImpressions":"110000"}}]}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	spec := integrations.ForecastSpec{
		Platform:    "dv360",
		AdAccountID: "a1",
		DailyBudget: 10,
		Currency:    "USD",
		StartDate:   start.Format("2006-01-02"),
		EndDate:     end.Format("2006-01-02"),
		Targeting: map[string]any{
			"geo":     []any{"US"},
			"age_min": float64(18),
			"age_max": float64(34),
			"genders": []any{"female"},
			"devices": []any{"mobile"},
		},
	}
	res, err := newTestClient(srv.URL).EstimateDelivery(context.Background(), "rt",
		integrations.PlatformAccount{AccountID: "a1"}, spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.EstimatedSpend != 110 || res.EstimatedReach != 55000 || res.EstimatedImpressions != 110000 || res.EstimatedCPM != 1 {
		t.Errorf("forecast = %+v", res)
	}

	targeting, _ := forecastBody["targeting"].(map[string]any)
	if targeting["ageRange"] != "PLANNABLE_AGE_RANGE_18_34" {
		t.Errorf("age range = %v", targeting["ageRange"])
	}
	locations, _ := targeting["plannableLocationIds"].([]any)
	if len(locations) != 1 || locations[0] != "loc1" {
		t.Errorf("locations = %v", targeting["plannableLocationIds"])
	}
	products, _ := forecastBody["plannedProducts"].([]any)
	product, _ := products[0].(map[string]any)
	if product["budgetMicros"] != "110000000" {
		t.Errorf("budget = %v", product["budgetMicros"])
	}
}

func TestListAudiences(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case r.URL.Path == "/v5/googleAudiences":
			if r.URL.Query().Get("advertiserId") != "a1" {
				t.Errorf("advertiserId = %q", r.URL.Query().Get("advertiserId"))
			}
			_, _ = w.Write([]byte(`{"googleAudiences":[{"googleAudienceId":"g1","displayName":"Affinity","googleAudienceType":"GOOGLE_AUDIENCE_TYPE_AFFINITY"}],"nextPageToken":"n1"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	page, err := newTestClient(srv.URL).ListAudiences(context.Background(), "rt", "a1", "google", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Audiences) != 1 || page.Audiences[0].ID != "g1" || page.Audiences[0].Type != "GOOGLE_AUDIENCE_TYPE_AFFINITY" {
		t.Fatalf("page = %+v", page)
	}
	if page.NextPageToken != "n1" {
		t.Errorf("next = %q", page.NextPageToken)
	}
}

func TestCreateLookalikeAudience(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case r.URL.Path == "/v5/lookalikeAudiences":
			if r.URL.Query().Get("advertiserId") != "a1" {
				t.Errorf("advertiserId = %q", r.URL.Query().Get("advertiserId"))
			}
			body = readBody(t, r)
			_, _ = w.Write([]byte(`{"lookalikeAudienceId":"lk1","displayName":"Look"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	aud, err := newTestClient(srv.URL).CreateLookalikeAudience(context.Background(), "rt", "a1",
		"Look", "MOST_REACH", []string{"seed1"}, []string{"US"})
	if err != nil {
		t.Fatal(err)
	}
	if aud.ID != "lk1" {
		t.Errorf("audience = %+v", aud)
	}
	if body["reachLevel"] != "MOST_REACH" {
		t.Errorf("body = %v", body)
	}
}

func TestUploadCustomBiddingScript(t *testing.T) {
	var uploaded string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, ":uploadScript"):
			_, _ = w.Write([]byte(`{"resourceName":"customBiddingAlgorithms/algo1/scriptRef/ref1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/upload/v5/customBiddingAlgorithms/algo1/scriptRef/ref1":
			b, _ := io.ReadAll(r.Body)
			uploaded = string(b)
			if r.Header.Get("Content-Type") != "application/javascript" {
				t.Errorf("content type = %q", r.Header.Get("Content-Type"))
			}
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v5/customBiddingAlgorithms/algo1/scripts":
			_, _ = w.Write([]byte(`{"customBiddingScriptId":"s1","state":"PENDING"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	script, err := newTestClient(srv.URL).UploadCustomBiddingScript(context.Background(), "rt", "a1", "algo1", "function score(){return 1;}")
	if err != nil {
		t.Fatal(err)
	}
	if script.ID != "s1" || script.State != "PENDING" {
		t.Errorf("script = %+v", script)
	}
	if !strings.Contains(uploaded, "function score") {
		t.Errorf("uploaded = %q", uploaded)
	}
}

func TestUploadCustomBiddingRules(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, ":uploadRules"):
			_, _ = w.Write([]byte(`{"resourceName":"customBiddingAlgorithms/algo1/rulesRef/ref1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/upload/v5/customBiddingAlgorithms/algo1/rulesRef/ref1":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v5/customBiddingAlgorithms/algo1/rules":
			_, _ = w.Write([]byte(`{"customBiddingAlgorithmRulesId":"r1","state":"ACCEPTED"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	rule, err := newTestClient(srv.URL).UploadCustomBiddingRules(context.Background(), "rt", "a1", "algo1", []byte(`{"rules":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if rule.ID != "r1" || rule.State != "ACCEPTED" {
		t.Errorf("rule = %+v", rule)
	}
}

func TestFetchInsightsGCSFallback(t *testing.T) {
	csvBody := "Date,Impressions,Clicks,Revenue (Adv Currency),Total Conversions\n2026-10-01,100,1,2.5,0\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/v2/queries":
			_, _ = w.Write([]byte(`{"queryId":"q1"}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, ":run"):
			_, _ = w.Write([]byte(`{"key":{"reportId":"r1"},"metadata":{"status":{"state":"DONE"}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/queries/q1/reports/r1" && r.URL.Query().Get("alt") == "media":
			http.Error(w, "no media", http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/queries/q1/reports/r1":
			_, _ = w.Write([]byte(`{"metadata":{"googleCloudStoragePath":"gs://bucket/report.csv"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/bucket/report.csv":
			_, _ = w.Write([]byte(csvBody))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.RawQuery+" "+r.URL.Path)
		}
	}))
	defer srv.Close()

	insights, err := newTestClient(srv.URL).FetchInsights(context.Background(), "rt", "a1", "c1", "2026-10-01", "2026-10-02")
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 || insights[0].Spend != 2.5 || insights[0].Impressions != 100 {
		t.Fatalf("insights = %+v", insights)
	}
}

func TestCreateAdvertiser(t *testing.T) {
	var body map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/v5/advertisers":
			path = r.URL.Path
			body = readBody(t, r)
			_, _ = w.Write([]byte(`{"advertiserId":"adv1"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	id, err := newTestClient(srv.URL).CreateAdvertiser(context.Background(), "rt", "p1", "bp1",
		"Lagos Clinic", "https://lagosclinic.example", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if id != "adv1" || path != "/v5/advertisers" {
		t.Fatalf("id=%q path=%q", id, path)
	}
	if body["partnerId"] != "p1" || body["displayName"] != "Lagos Clinic" || body["entityStatus"] != "ENTITY_STATUS_ACTIVE" {
		t.Errorf("body = %v", body)
	}
	general, _ := body["generalConfig"].(map[string]any)
	if general["currencyCode"] != "USD" || general["domainUrl"] != "https://lagosclinic.example" {
		t.Errorf("general = %v", general)
	}
	billing, _ := body["billingConfig"].(map[string]any)
	if billing["billingProfileId"] != "bp1" {
		t.Errorf("billing = %v", billing)
	}
}
