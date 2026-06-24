package meta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	row := InsightRow{
		DateStart:       "2024-03-15",
		DateStop:        "2024-03-15",
		Spend:           "1234.56",
		Impressions:     "10000",
		Clicks:          "250",
		Reach:           "8000",
		Cpm:             "12.34",
		Cpc:             "4.93",
		Ctr:             "2.5",
		AccountCurrency: "USD",
		Actions: []InsightAction{
			{ActionType: "link_click", Value: "250"},
			{ActionType: "purchase", Value: "5"},
			{ActionType: "omni_purchase", Value: "7.4"},
		},
		PurchaseROAS: []InsightAction{
			{ActionType: "omni_purchase", Value: "3.25"},
		},
	}

	n := Normalize(row)

	if got := n.Date.Format("2006-01-02"); got != "2024-03-15" {
		t.Errorf("date: got %q", got)
	}
	if n.Spend != 1234.56 {
		t.Errorf("spend: got %v", n.Spend)
	}
	if n.Impressions != 10000 || n.Clicks != 250 || n.Reach != 8000 {
		t.Errorf("counts: imp=%d clicks=%d reach=%d", n.Impressions, n.Clicks, n.Reach)
	}
	if n.CPM != 12.34 || n.CPC != 4.93 || n.CTR != 2.5 {
		t.Errorf("ratios: cpm=%v cpc=%v ctr=%v", n.CPM, n.CPC, n.CTR)
	}
	// omni_purchase has higher priority than purchase; 7.4 rounds to 7.
	if n.Conversions != 7 {
		t.Errorf("conversions: got %d want 7", n.Conversions)
	}
	if n.ROAS != 3.25 {
		t.Errorf("roas: got %v want 3.25", n.ROAS)
	}
	if n.Currency != "USD" {
		t.Errorf("currency: got %q", n.Currency)
	}
}

func TestNormalizeDefaults(t *testing.T) {
	n := Normalize(InsightRow{DateStart: "2024-01-01"})
	if n.Currency != "NGN" {
		t.Errorf("expected default currency NGN, got %q", n.Currency)
	}
	if n.Conversions != 0 || n.ROAS != 0 || n.Spend != 0 {
		t.Errorf("expected zero metrics, got %+v", n)
	}
}

func TestNormalizeConversionRounding(t *testing.T) {
	n := Normalize(InsightRow{
		DateStart: "2024-01-01",
		Actions:   []InsightAction{{ActionType: "lead", Value: "3.6"}},
	})
	if n.Conversions != 4 {
		t.Errorf("conversions: got %d want 4", n.Conversions)
	}
}

func TestGetCampaigns(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"data":[{"id":"23847","name":"Spring Sale","objective":"OUTCOME_SALES","status":"ACTIVE","daily_budget":"5000"}]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	campaigns, err := c.GetCampaigns(context.Background(), "tok", "123456")
	if err != nil {
		t.Fatalf("GetCampaigns failed: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/act_123456/campaigns") {
		t.Errorf("path: got %q want suffix /act_123456/campaigns", gotPath)
	}
	if len(campaigns) != 1 {
		t.Fatalf("expected 1 campaign, got %d", len(campaigns))
	}
	if campaigns[0].ID != "23847" || campaigns[0].Name != "Spring Sale" || campaigns[0].DailyBudget != "5000" {
		t.Errorf("unexpected campaign: %+v", campaigns[0])
	}
}

func TestGetCampaignsDoesNotDoublePrefix(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).GetCampaigns(context.Background(), "tok", "act_999")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(gotPath, "/act_999/campaigns") || strings.Contains(gotPath, "act_act_") {
		t.Errorf("path: got %q", gotPath)
	}
}

func TestGetCampaignsPagination(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/page2", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"B"}]}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"A"}],"paging":{"next":"` + srv.URL + `/page2"}}`))
	})
	srv.Config.Handler = mux

	campaigns, err := newTestClient(srv.URL).GetCampaigns(context.Background(), "tok", "1")
	if err != nil {
		t.Fatalf("GetCampaigns failed: %v", err)
	}
	if len(campaigns) != 2 || campaigns[0].ID != "A" || campaigns[1].ID != "B" {
		t.Fatalf("pagination failed: %+v", campaigns)
	}
}

func TestGetInsights(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"data":[
			{"date_start":"2024-03-01","date_stop":"2024-03-01","spend":"10.00","impressions":"100","clicks":"5","account_currency":"USD"},
			{"date_start":"2024-03-02","date_stop":"2024-03-02","spend":"20.00","impressions":"200","clicks":"9","account_currency":"USD"}
		]}`))
	}))
	defer srv.Close()

	rows, err := newTestClient(srv.URL).GetInsights(context.Background(), "tok", "23847", "2024-03-01", "2024-03-30")
	if err != nil {
		t.Fatalf("GetInsights failed: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/23847/insights") {
		t.Errorf("path: got %q", gotPath)
	}
	if !strings.Contains(gotQuery, "time_increment=1") {
		t.Errorf("query missing time_increment=1: %q", gotQuery)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[1].DateStart != "2024-03-02" || rows[1].Spend != "20.00" {
		t.Errorf("unexpected row: %+v", rows[1])
	}
}

func TestGetInsightsGraphError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"(#100) Invalid parameter","type":"OAuthException","code":100}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).GetInsights(context.Background(), "tok", "c1", "2024-03-01", "2024-03-30")
	if err == nil {
		t.Fatal("expected error from graph error response")
	}
}
