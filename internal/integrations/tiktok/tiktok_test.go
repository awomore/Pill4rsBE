package tiktok

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

func newTestClient(serverURL string) *Client {
	c := NewClient("app123", "secret", "https://api.example.com/api/integrations/tiktok/callback")
	c.baseURL = serverURL
	return c
}

func TestGetOAuthURL(t *testing.T) {
	raw := NewClient("app123", "secret", "https://api.example.com/cb").GetOAuthURL("state-xyz")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("invalid url: %v", err)
	}
	if !strings.HasSuffix(u.Path, "/portal/auth") {
		t.Errorf("path: %q", u.Path)
	}
	q := u.Query()
	if q.Get("app_id") != "app123" || q.Get("state") != "state-xyz" || q.Get("redirect_uri") != "https://api.example.com/cb" {
		t.Errorf("query: %v", q)
	}
}

func TestExchangeCodeForToken(t *testing.T) {
	var body map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Write([]byte(`{"code":0,"message":"OK","data":{"access_token":"tok","advertiser_ids":["123"]}}`))
	}))
	defer srv.Close()

	tok, err := newTestClient(srv.URL).ExchangeCodeForToken(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("ExchangeCodeForToken failed: %v", err)
	}
	if tok.AccessToken != "tok" {
		t.Errorf("access token: %q", tok.AccessToken)
	}
	if !strings.HasSuffix(path, "/oauth2/access_token/") {
		t.Errorf("path: %q", path)
	}
	if body["auth_code"] != "the-code" || body["app_id"] != "app123" || body["grant_type"] != "authorization_code" {
		t.Errorf("body: %v", body)
	}
}

func TestExchangeCodeForTokenError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":40001,"message":"invalid auth_code"}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).ExchangeCodeForToken(context.Background(), "bad")
	if err == nil || !strings.Contains(err.Error(), "invalid auth_code") {
		t.Fatalf("expected api error, got %v", err)
	}
}

func TestFetchAdvertiserID(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("Access-Token")
		w.Write([]byte(`{"code":0,"data":{"list":[{"advertiser_id":"adv1","advertiser_name":"Acme"}]}}`))
	}))
	defer srv.Close()

	id, err := newTestClient(srv.URL).FetchAdvertiserID(context.Background(), "tok")
	if err != nil || id != "adv1" {
		t.Fatalf("FetchAdvertiserID = %q, err %v", id, err)
	}
	if gotToken != "tok" {
		t.Errorf("Access-Token header = %q", gotToken)
	}
}

func deliverableSpec() integrations.CampaignSpec {
	return integrations.CampaignSpec{
		Name:        "Summer Launch",
		Objective:   integrations.ObjectiveSales,
		DailyBudget: 50,
		CTA:         "SHOP_NOW",
		Creative: &integrations.CreativeSpec{
			PrimaryText: "Shop the sale",
			LinkURL:     "https://shop.example.com",
			ImageURL:    "https://cdn.example.com/a.jpg",
		},
	}
}

func TestEnsureDeliverableFullTree(t *testing.T) {
	hits := map[string]int{}
	var campaignBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Access-Token") != "tok" {
			t.Errorf("missing Access-Token header on %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/campaign/create/"):
			hits["campaign"]++
			_ = json.Unmarshal(raw, &campaignBody)
			w.Write([]byte(`{"code":0,"data":{"campaign_id":"c1"}}`))
		case strings.HasSuffix(r.URL.Path, "/adgroup/create/"):
			hits["adgroup"]++
			w.Write([]byte(`{"code":0,"data":{"adgroup_id":"ag1"}}`))
		case strings.HasSuffix(r.URL.Path, "/file/image/ad/upload/"):
			hits["image"]++
			w.Write([]byte(`{"code":0,"data":{"image_id":"img1"}}`))
		case strings.HasSuffix(r.URL.Path, "/ad/create/"):
			hits["ad"]++
			w.Write([]byte(`{"code":0,"data":{"ad_ids":["ad1"]}}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	account := integrations.PlatformAccount{AccountID: "adv1"}
	state, err := newTestClient(srv.URL).EnsureDeliverable(context.Background(), "tok", account, deliverableSpec(), integrations.DeliverableState{})
	if err != nil {
		t.Fatalf("EnsureDeliverable failed: %v", err)
	}
	want := integrations.DeliverableState{CampaignID: "c1", AdSetID: "ag1", CreativeID: "img1", AdID: "ad1"}
	if state != want {
		t.Errorf("state = %+v, want %+v", state, want)
	}
	for _, edge := range []string{"campaign", "adgroup", "image", "ad"} {
		if hits[edge] != 1 {
			t.Errorf("%s hit %d times", edge, hits[edge])
		}
	}
	if campaignBody["objective_type"] != "WEB_CONVERSIONS" || campaignBody["operation_status"] != "DISABLE" || campaignBody["advertiser_id"] != "adv1" {
		t.Errorf("campaign body = %v", campaignBody)
	}
}

func TestEnsureDeliverableResumes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/campaign/create/"):
			t.Error("should not re-create campaign")
		case strings.HasSuffix(r.URL.Path, "/adgroup/create/"):
			t.Error("should not re-create ad group")
		case strings.HasSuffix(r.URL.Path, "/file/image/ad/upload/"):
			w.Write([]byte(`{"code":0,"data":{"image_id":"img9"}}`))
		case strings.HasSuffix(r.URL.Path, "/ad/create/"):
			w.Write([]byte(`{"code":0,"data":{"ad_ids":["ad9"]}}`))
		}
	}))
	defer srv.Close()

	have := integrations.DeliverableState{CampaignID: "c1", AdSetID: "ag1"}
	state, err := newTestClient(srv.URL).EnsureDeliverable(context.Background(), "tok", integrations.PlatformAccount{AccountID: "adv1"}, deliverableSpec(), have)
	if err != nil {
		t.Fatalf("EnsureDeliverable failed: %v", err)
	}
	if state.CreativeID != "img9" || state.AdID != "ad9" || state.CampaignID != "c1" || state.AdSetID != "ag1" {
		t.Errorf("resume state = %+v", state)
	}
}

func TestSetStatus(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer srv.Close()

	if err := newTestClient(srv.URL).SetStatus(context.Background(), "tok", "adv1", "c1", "ACTIVE"); err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}
	if body["operation_status"] != "ENABLE" || body["advertiser_id"] != "adv1" {
		t.Errorf("body = %v", body)
	}
}

func TestUpdateAdSetBudget(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer srv.Close()

	if err := newTestClient(srv.URL).UpdateAdSetBudget(context.Background(), "tok", "adv1", "ag1", 75); err != nil {
		t.Fatalf("UpdateAdSetBudget failed: %v", err)
	}
	if body["adgroup_id"] != "ag1" || body["budget"] != float64(75) {
		t.Errorf("body = %v", body)
	}
}

func TestFetchCampaigns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"list":[{"campaign_id":"c1","campaign_name":"Sale","objective_type":"TRAFFIC","operation_status":"ENABLE","budget":50}]}}`))
	}))
	defer srv.Close()

	camps, err := newTestClient(srv.URL).FetchCampaigns(context.Background(), "tok", "adv1")
	if err != nil || len(camps) != 1 {
		t.Fatalf("FetchCampaigns = %+v, err %v", camps, err)
	}
	c := camps[0]
	if c.ExternalID != "c1" || c.Name != "Sale" || c.Status != "ACTIVE" || c.DailyBudget != 50 {
		t.Errorf("campaign = %+v", c)
	}
}

func TestFetchInsights(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"list":[{"dimensions":{"campaign_id":"c1","stat_time_day":"2026-07-01 00:00:00"},"metrics":{"spend":"10.5","impressions":"1000","clicks":"50","conversion":"3","reach":"800","cpc":"0.21","cpm":"10.5","ctr":"5.0"}}]}}`))
	}))
	defer srv.Close()

	rows, err := newTestClient(srv.URL).FetchInsights(context.Background(), "tok", "adv1", "c1", "2026-06-01", "2026-07-01")
	if err != nil || len(rows) != 1 {
		t.Fatalf("FetchInsights = %+v, err %v", rows, err)
	}
	r := rows[0]
	if r.Date.Format("2006-01-02") != "2026-07-01" {
		t.Errorf("date = %v", r.Date)
	}
	if r.Spend != 10.5 || r.Impressions != 1000 || r.Clicks != 50 || r.Conversions != 3 {
		t.Errorf("metrics = %+v", r)
	}
}
