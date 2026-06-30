package google

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
	c := NewClient("client-id", "client-secret", "https://api.example.com/cb", "dev-token", "")
	c.oauthBaseURL = serverURL
	c.adsBaseURL = serverURL
	return c
}

// tokenHandler writes the right token response based on grant_type.
func writeToken(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if r.Form.Get("grant_type") == "authorization_code" {
		w.Write([]byte(`{"access_token":"at0","refresh_token":"rt","expires_in":3600}`))
		return
	}
	w.Write([]byte(`{"access_token":"at","expires_in":3600}`))
}

func TestGetOAuthURL(t *testing.T) {
	u, err := url.Parse(NewClient("cid", "sec", "https://x/cb", "dev", "").GetOAuthURL("st"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != "cid" || q.Get("state") != "st" || q.Get("access_type") != "offline" || q.Get("prompt") != "consent" {
		t.Errorf("query: %v", q)
	}
	if !strings.Contains(q.Get("scope"), "adwords") {
		t.Errorf("scope: %q", q.Get("scope"))
	}
}

func TestExchangeCodeForToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeToken(w, r)
	}))
	defer srv.Close()

	tok, err := newTestClient(srv.URL).ExchangeCodeForToken(context.Background(), "code")
	if err != nil {
		t.Fatalf("ExchangeCodeForToken failed: %v", err)
	}
	if tok.AccessToken != "rt" { // we store the refresh token
		t.Errorf("expected refresh token, got %q", tok.AccessToken)
	}
}

func TestFetchCustomerID(t *testing.T) {
	var gotDevToken, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case strings.Contains(r.URL.Path, "listAccessibleCustomers"):
			gotDevToken = r.Header.Get("developer-token")
			gotAuth = r.Header.Get("Authorization")
			if r.Method != http.MethodPost {
				t.Errorf("listAccessibleCustomers must use POST, got %s", r.Method)
			}
			w.Write([]byte(`{"resourceNames":["customers/1234567890"]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	id, err := newTestClient(srv.URL).FetchCustomerID(context.Background(), "rt")
	if err != nil || id != "1234567890" {
		t.Fatalf("FetchCustomerID = %q, err %v", id, err)
	}
	if gotDevToken != "dev-token" || gotAuth != "Bearer at" {
		t.Errorf("headers: dev-token=%q auth=%q", gotDevToken, gotAuth)
	}
}

func deliverableSpec() integrations.CampaignSpec {
	return integrations.CampaignSpec{
		Name:        "Brand Search",
		Objective:   integrations.ObjectiveSales,
		DailyBudget: 50,
		Creative: &integrations.CreativeSpec{
			PrimaryText: "Shop the sale",
			Headline:    "Up to 50% off",
			LinkURL:     "https://shop.example.com",
		},
	}
}

func TestEnsureDeliverableFullTree(t *testing.T) {
	hits := map[string]int{}
	var campaignCreate map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
			return
		case strings.Contains(r.URL.Path, "campaignBudgets:mutate"):
			hits["budget"]++
			w.Write([]byte(`{"results":[{"resourceName":"customers/123/campaignBudgets/11"}]}`))
		case strings.Contains(r.URL.Path, "campaigns:mutate"):
			hits["campaign"]++
			var op struct {
				Operations []struct {
					Create map[string]any `json:"create"`
				} `json:"operations"`
			}
			_ = json.Unmarshal(raw, &op)
			if len(op.Operations) > 0 {
				campaignCreate = op.Operations[0].Create
			}
			w.Write([]byte(`{"results":[{"resourceName":"customers/123/campaigns/22"}]}`))
		case strings.Contains(r.URL.Path, "adGroupAds:mutate"):
			hits["ad"]++
			w.Write([]byte(`{"results":[{"resourceName":"customers/123/adGroupAds/44"}]}`))
		case strings.Contains(r.URL.Path, "adGroups:mutate"):
			hits["adgroup"]++
			w.Write([]byte(`{"results":[{"resourceName":"customers/123/adGroups/33"}]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("developer-token") != "dev-token" {
			t.Errorf("missing developer-token on %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	account := integrations.PlatformAccount{AccountID: "123"}
	state, err := newTestClient(srv.URL).EnsureDeliverable(context.Background(), "rt", account, deliverableSpec(), integrations.DeliverableState{})
	if err != nil {
		t.Fatalf("EnsureDeliverable failed: %v", err)
	}
	if state.CampaignID != "22" || state.AdSetID != "33" || state.AdID != "44" {
		t.Errorf("state = %+v", state)
	}
	for _, edge := range []string{"budget", "campaign", "adgroup", "ad"} {
		if hits[edge] != 1 {
			t.Errorf("%s hit %d times", edge, hits[edge])
		}
	}
	if campaignCreate["advertisingChannelType"] != "SEARCH" || campaignCreate["status"] != "PAUSED" {
		t.Errorf("campaign create = %v", campaignCreate)
	}
}

func TestEnsureDeliverableResumes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case strings.Contains(r.URL.Path, "campaignBudgets:mutate"), strings.Contains(r.URL.Path, "campaigns:mutate"), strings.Contains(r.URL.Path, "adGroups:mutate"):
			t.Errorf("should not hit %q on resume", r.URL.Path)
		case strings.Contains(r.URL.Path, "adGroupAds:mutate"):
			w.Write([]byte(`{"results":[{"resourceName":"customers/123/adGroupAds/99"}]}`))
		}
	}))
	defer srv.Close()

	have := integrations.DeliverableState{CampaignID: "22", AdSetID: "33"}
	state, err := newTestClient(srv.URL).EnsureDeliverable(context.Background(), "rt", integrations.PlatformAccount{AccountID: "123"}, deliverableSpec(), have)
	if err != nil {
		t.Fatalf("EnsureDeliverable failed: %v", err)
	}
	if state.AdID != "99" || state.CampaignID != "22" || state.AdSetID != "33" {
		t.Errorf("resume state = %+v", state)
	}
}

func TestSetStatus(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case strings.Contains(r.URL.Path, "campaigns:mutate"):
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			w.Write([]byte(`{"results":[{"resourceName":"customers/123/campaigns/22"}]}`))
		}
	}))
	defer srv.Close()

	if err := newTestClient(srv.URL).SetStatus(context.Background(), "rt", "123", "22", "ACTIVE"); err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}
	ops, _ := body["operations"].([]any)
	if len(ops) == 0 {
		t.Fatalf("no operations in %v", body)
	}
	op := ops[0].(map[string]any)
	if op["updateMask"] != "status" {
		t.Errorf("updateMask = %v", op["updateMask"])
	}
	upd := op["update"].(map[string]any)
	if upd["status"] != "ENABLED" || upd["resourceName"] != "customers/123/campaigns/22" {
		t.Errorf("update = %v", upd)
	}
}

func TestUpdateAdSetBudgetNotSupported(t *testing.T) {
	if err := newTestClient("http://unused").UpdateAdSetBudget(context.Background(), "rt", "123", "33", 50); err == nil {
		t.Fatal("expected not-supported error")
	}
}

func TestFetchCampaigns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case strings.Contains(r.URL.Path, "googleAds:search"):
			w.Write([]byte(`{"results":[{"campaign":{"id":"22","name":"Brand Search","status":"ENABLED"},"campaignBudget":{"amountMicros":"50000000"}}]}`))
		}
	}))
	defer srv.Close()

	camps, err := newTestClient(srv.URL).FetchCampaigns(context.Background(), "rt", "123")
	if err != nil || len(camps) != 1 {
		t.Fatalf("FetchCampaigns = %+v, err %v", camps, err)
	}
	c := camps[0]
	if c.ExternalID != "22" || c.Name != "Brand Search" || c.Status != "ACTIVE" || c.DailyBudget != 50 {
		t.Errorf("campaign = %+v", c)
	}
}

func TestFetchInsights(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			writeToken(w, r)
		case strings.Contains(r.URL.Path, "googleAds:search"):
			w.Write([]byte(`{"results":[{"segments":{"date":"2026-07-01"},"metrics":{"costMicros":"10500000","impressions":"1000","clicks":"50","conversions":3.0,"ctr":0.05,"averageCpc":"210000","averageCpm":"10500000"}}]}`))
		}
	}))
	defer srv.Close()

	rows, err := newTestClient(srv.URL).FetchInsights(context.Background(), "rt", "123", "22", "2026-06-01", "2026-07-01")
	if err != nil || len(rows) != 1 {
		t.Fatalf("FetchInsights = %+v, err %v", rows, err)
	}
	r := rows[0]
	if r.Date.Format("2006-01-02") != "2026-07-01" {
		t.Errorf("date = %v", r.Date)
	}
	if r.Spend != 10.5 || r.Impressions != 1000 || r.Clicks != 50 || r.Conversions != 3 || r.CTR != 5.0 || r.CPC != 0.21 {
		t.Errorf("metrics = %+v", r)
	}
}
