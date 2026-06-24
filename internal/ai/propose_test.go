package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProposeCampaign(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &reqBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"content": [
				{"type":"text","text":"Here is a campaign:"},
				{"type":"tool_use","name":"submit_campaign","input":{
					"name":"Summer Sale",
					"objective":"sales",
					"daily_budget":5000,
					"currency":"NGN",
					"start_date":"2026-07-01",
					"cta":"SHOP_NOW",
					"platforms":["meta"],
					"targeting":{"geo":["NG"],"age_min":18},
					"creative":{"primary_text":"Big summer sale","headline":"50% off","link_url":"https://shop.example.com","image_url":"https://cdn.example.com/a.jpg"}
				}}
			]
		}`))
	}))
	defer srv.Close()

	c := NewClient("test-key")
	c.baseURL = srv.URL

	pc, err := c.ProposeCampaign(context.Background(), "system", "make me a summer sale campaign")
	if err != nil {
		t.Fatalf("ProposeCampaign failed: %v", err)
	}

	if pc.Name != "Summer Sale" || pc.Objective != "sales" || pc.DailyBudget != 5000 {
		t.Errorf("unexpected campaign: %+v", pc)
	}
	if len(pc.Platforms) != 1 || pc.Platforms[0] != "meta" {
		t.Errorf("platforms: %+v", pc.Platforms)
	}
	if pc.Creative == nil || pc.Creative.LinkURL != "https://shop.example.com" || pc.Creative.ImageURL == "" {
		t.Errorf("creative: %+v", pc.Creative)
	}
	if pc.Targeting["geo"] == nil {
		t.Errorf("targeting: %+v", pc.Targeting)
	}

	// The model must be forced to call submit_campaign.
	tc, _ := reqBody["tool_choice"].(map[string]any)
	if tc["type"] != "tool" || tc["name"] != "submit_campaign" {
		t.Errorf("tool_choice not forced: %v", reqBody["tool_choice"])
	}
	if reqBody["tools"] == nil {
		t.Error("request missing tools")
	}
}

func TestProposeCampaignNoToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"content":[{"type":"text","text":"I cannot do that"}]}`))
	}))
	defer srv.Close()

	c := NewClient("k")
	c.baseURL = srv.URL
	if _, err := c.ProposeCampaign(context.Background(), "s", "hi"); err == nil {
		t.Fatal("expected error when no tool_use block is returned")
	}
}

func TestProposeCampaignAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer srv.Close()

	c := NewClient("k")
	c.baseURL = srv.URL
	_, err := c.ProposeCampaign(context.Background(), "s", "hi")
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected api error, got %v", err)
	}
}
