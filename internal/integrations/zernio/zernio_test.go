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

func TestCreateProfileUnwrapsEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/profiles" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"message":"Profile created successfully","profile":{"_id":"66a1f0c2a4b9d3e8f1a2b3c4","name":"Acme"}}`))
	}))
	defer srv.Close()

	c := NewClient("k", srv.URL)
	p, err := c.CreateProfile(context.Background(), "Acme")
	if err != nil {
		t.Fatalf("CreateProfile error: %v", err)
	}
	if p.ID != "66a1f0c2a4b9d3e8f1a2b3c4" || p.Name != "Acme" {
		t.Fatalf("profile = %+v", p)
	}
}

func TestAdsConnectURLSendsProfileID(t *testing.T) {
	var path, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		query = r.URL.RawQuery
		w.Write([]byte(`{"authUrl":"https://example.com/oauth","state":"abc"}`))
	}))
	defer srv.Close()

	c := NewClient("k", srv.URL)
	res, err := c.AdsConnectURL(context.Background(), "linkedin", "prof1", "http://localhost:8080/cb")
	if err != nil {
		t.Fatalf("AdsConnectURL error: %v", err)
	}
	if !strings.Contains(path, "/v1/connect/linkedin/ads") {
		t.Errorf("path = %q", path)
	}
	if !strings.Contains(query, "profileId=prof1") || !strings.Contains(query, "redirect_url=") {
		t.Errorf("query = %q", query)
	}
	if res.AuthURL != "https://example.com/oauth" {
		t.Errorf("authUrl = %q", res.AuthURL)
	}
}

func TestListAccountsAcceptsObjectProfileID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"accounts":[
			{"_id":"a1","platform":"linkedin","profileId":{"_id":"p1","name":"Acme"},"username":"acme","isActive":true},
			{"_id":"a2","platform":"twitter","profileId":"p2","username":"acme2","isActive":false}
		]}`))
	}))
	defer srv.Close()

	c := NewClient("k", srv.URL)
	accounts, err := c.ListAccounts(context.Background(), "p1")
	if err != nil {
		t.Fatalf("ListAccounts error: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("accounts = %+v", accounts)
	}
	if string(accounts[0].ProfileID) != "p1" || string(accounts[1].ProfileID) != "p2" {
		t.Fatalf("profile ids = %q, %q", accounts[0].ProfileID, accounts[1].ProfileID)
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
