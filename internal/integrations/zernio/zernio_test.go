package zernio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListCampaigns(t *testing.T) {
	var auth, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		query = r.URL.RawQuery
		w.Write([]byte(`{"campaigns":[{"platformCampaignId":"123","platform":"linkedin","campaignName":"Launch","status":"active","currency":"USD","platformObjective":"LEAD_GENERATION","budget":{"amount":50,"type":"daily"},"metrics":{"spend":12.5,"clicks":3,"impressions":100,"conversions":1,"roas":2.4}}]}`))
	}))
	defer srv.Close()

	c := NewClient("key123", srv.URL)
	camps, err := c.ListCampaigns(context.Background(), "acct1", "", "2026-01-01", "2026-01-31")
	if err != nil {
		t.Fatalf("ListCampaigns error: %v", err)
	}
	if auth != "Bearer key123" {
		t.Errorf("authorization = %q", auth)
	}
	if !strings.Contains(query, "accountId=acct1") || !strings.Contains(query, "fromDate=2026-01-01") {
		t.Errorf("query = %q", query)
	}
	if len(camps) != 1 || camps[0].PlatformCampaignID != "123" || camps[0].Budget == nil || camps[0].Budget.Amount != 50 {
		t.Fatalf("campaigns = %+v", camps)
	}
}

func TestAdapterFetchCampaigns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"campaigns":[{"platformCampaignId":"c1","campaignName":"Spring","status":"paused","budget":{"amount":25,"type":"daily"}}]}`))
	}))
	defer srv.Close()

	a := &Adapter{client: NewClient("k", srv.URL), platform: PlatformLinkedInAds, display: "linkedin"}
	got, err := a.FetchCampaigns(context.Background(), "", "acct")
	if err != nil {
		t.Fatalf("FetchCampaigns error: %v", err)
	}
	if len(got) != 1 || got[0].ExternalID != "c1" || got[0].Status != "PAUSED" || got[0].DailyBudget != 25 {
		t.Fatalf("normalized = %+v", got)
	}
}

func TestAdapterFetchInsights(t *testing.T) {
	var path, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		query = r.URL.RawQuery
		w.Write([]byte(`{"analytics":{"summary":{"spend":5},"daily":[{"date":"2026-01-02","spend":5,"impressions":50,"clicks":2,"conversions":1,"reach":40,"cpc":2.5,"cpm":100,"ctr":4,"roas":1.5}]}}`))
	}))
	defer srv.Close()

	a := &Adapter{client: NewClient("k", srv.URL), platform: PlatformXAds, display: "twitter"}
	got, err := a.FetchInsights(context.Background(), "", "acct", "camp1", "2026-01-01", "2026-01-31")
	if err != nil {
		t.Fatalf("FetchInsights error: %v", err)
	}
	if !strings.Contains(path, "/v1/ads/campaigns/camp1/analytics") {
		t.Errorf("path = %q", path)
	}
	if !strings.Contains(query, "platform=twitter") {
		t.Errorf("query = %q", query)
	}
	if len(got) != 1 || got[0].Spend != 5 || got[0].Impressions != 50 || got[0].Date.Format("2006-01-02") != "2026-01-02" {
		t.Fatalf("insights = %+v", got)
	}
}

func TestAdapterSetStatus(t *testing.T) {
	var method, path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Write([]byte(`{"status":"paused","updated":1}`))
	}))
	defer srv.Close()

	a := &Adapter{client: NewClient("k", srv.URL), platform: PlatformPinterestAds, display: "pinterest"}
	if err := a.SetStatus(context.Background(), "", "acct", "camp9", "ACTIVE"); err != nil {
		t.Fatalf("SetStatus error: %v", err)
	}
	if method != http.MethodPut || !strings.Contains(path, "/v1/ads/campaigns/camp9/status") {
		t.Errorf("method/path = %s %s", method, path)
	}
	if body["status"] != "active" || body["platform"] != "pinterest" {
		t.Errorf("body = %v", body)
	}
}

func TestCreateAdParsesTree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"ad":{"_id":"ad1","platformCampaignId":"camp1","platformAdSetId":"set1"}}`))
	}))
	defer srv.Close()

	c := NewClient("k", srv.URL)
	res, err := c.CreateAd(context.Background(), map[string]any{"name": "x"})
	if err != nil {
		t.Fatalf("CreateAd error: %v", err)
	}
	if res.AdID != "ad1" || res.PlatformCampaignID != "camp1" || res.PlatformAdSetID != "set1" {
		t.Fatalf("result = %+v", res)
	}
}

func TestNewAdaptersCoversAllNetworks(t *testing.T) {
	adapters := NewAdapters(NewClient("k", "http://x"))
	seen := map[string]bool{}
	for _, a := range adapters {
		seen[a.Platform()] = true
	}
	for _, want := range []string{PlatformMetaAds, PlatformGoogleAds, PlatformTikTokAds, PlatformLinkedInAds, PlatformPinterestAds, PlatformXAds, PlatformOpenAIAds} {
		if !seen[want] {
			t.Errorf("missing adapter for %q", want)
		}
	}
	if DisplayPlatform(PlatformXAds) != "twitter" || DisplayPlatform(PlatformMetaAds) != "facebook" {
		t.Error("DisplayPlatform mapping wrong")
	}
	if !IsAdsPlatform(PlatformLinkedInAds) || IsAdsPlatform("instagram") {
		t.Error("IsAdsPlatform wrong")
	}
}
