package meta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

func TestPlatform(t *testing.T) {
	if got := NewClient("a", "b", "c").Platform(); got != "meta" {
		t.Errorf("Platform() = %q, want meta", got)
	}
}

func TestCreateCampaign(t *testing.T) {
	var gotPath, gotMethod, gotCT string
	var form map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		form = map[string]string{
			"name":                  r.Form.Get("name"),
			"objective":             r.Form.Get("objective"),
			"status":                r.Form.Get("status"),
			"special_ad_categories": r.Form.Get("special_ad_categories"),
			"daily_budget":          r.Form.Get("daily_budget"),
			"access_token":          r.Form.Get("access_token"),
		}
		w.Write([]byte(`{"id":"23847000111"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	res, err := c.CreateCampaign(context.Background(), "tok", "123456", integrations.CampaignSpec{
		Name:        "Summer Launch",
		Objective:   integrations.ObjectiveSales,
		DailyBudget: 50.50,
		Currency:    "NGN",
	})
	if err != nil {
		t.Fatalf("CreateCampaign failed: %v", err)
	}

	if res.ExternalID != "23847000111" || res.Status != "PAUSED" {
		t.Errorf("unexpected result: %+v", res)
	}
	if !strings.HasSuffix(gotPath, "/act_123456/campaigns") {
		t.Errorf("path: got %q", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q want POST", gotMethod)
	}
	if !strings.HasPrefix(gotCT, "application/x-www-form-urlencoded") {
		t.Errorf("content-type: got %q", gotCT)
	}

	want := map[string]string{
		"name":                  "Summer Launch",
		"objective":             "OUTCOME_SALES",
		"status":                "PAUSED",
		"special_ad_categories": "[]",
		"daily_budget":          "5050", // 50.50 major -> 5050 minor units
		"access_token":          "tok",
	}
	for k, v := range want {
		if form[k] != v {
			t.Errorf("form[%q] = %q, want %q", k, form[k], v)
		}
	}
}

func TestCreateCampaignOmitsZeroBudget(t *testing.T) {
	var hadBudget bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		_, hadBudget = r.Form["daily_budget"]
		w.Write([]byte(`{"id":"1"}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).CreateCampaign(context.Background(), "tok", "1", integrations.CampaignSpec{
		Name: "No budget", Objective: integrations.ObjectiveTraffic,
	})
	if err != nil {
		t.Fatal(err)
	}
	if hadBudget {
		t.Error("expected no daily_budget field when budget is zero")
	}
}

func TestCreateCampaignGraphError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"Invalid parameter","type":"OAuthException","code":100}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).CreateCampaign(context.Background(), "tok", "1", integrations.CampaignSpec{
		Name: "X", Objective: integrations.ObjectiveSales, DailyBudget: 10,
	})
	if err == nil || !strings.Contains(err.Error(), "Invalid parameter") {
		t.Fatalf("expected graph error, got %v", err)
	}
}

func TestCreateCampaignUnsupportedObjective(t *testing.T) {
	// No server should be hit — objective is rejected before the HTTP call.
	_, err := newTestClient("http://127.0.0.1:0").CreateCampaign(context.Background(), "tok", "1", integrations.CampaignSpec{
		Name: "X", Objective: integrations.CampaignObjective("bogus"), DailyBudget: 10,
	})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected unsupported objective error, got %v", err)
	}
}
