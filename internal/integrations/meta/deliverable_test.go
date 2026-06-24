package meta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

func deliverableSpec() integrations.CampaignSpec {
	return integrations.CampaignSpec{
		Name:        "Summer Launch",
		Objective:   integrations.ObjectiveSales,
		DailyBudget: 50,
		Targeting:   map[string]any{"geo": []any{"NG"}},
		Creative: &integrations.CreativeSpec{
			PrimaryText: "Shop the sale",
			Headline:    "Up to 50% off",
			LinkURL:     "https://example.com/sale",
			ImageURL:    "https://example.com/img.jpg",
		},
	}
}

func deliverableAccount() integrations.PlatformAccount {
	return integrations.PlatformAccount{AccountID: "123", PageID: "page_1", PixelID: "pixel_1"}
}

func TestEnsureDeliverableFullTree(t *testing.T) {
	hits := map[string]int{}
	var adsetForm map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch {
		case strings.HasSuffix(r.URL.Path, "/campaigns"):
			hits["campaigns"]++
			w.Write([]byte(`{"id":"camp_1"}`))
		case strings.HasSuffix(r.URL.Path, "/adsets"):
			hits["adsets"]++
			adsetForm = map[string]string{
				"campaign_id":       r.Form.Get("campaign_id"),
				"optimization_goal": r.Form.Get("optimization_goal"),
				"status":            r.Form.Get("status"),
				"targeting":         r.Form.Get("targeting"),
				"promoted_object":   r.Form.Get("promoted_object"),
			}
			w.Write([]byte(`{"id":"adset_1"}`))
		case strings.HasSuffix(r.URL.Path, "/adcreatives"):
			hits["adcreatives"]++
			w.Write([]byte(`{"id":"cr_1"}`))
		case strings.HasSuffix(r.URL.Path, "/ads"):
			hits["ads"]++
			w.Write([]byte(`{"id":"ad_1"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	state, err := newTestClient(srv.URL).EnsureDeliverable(context.Background(), "tok", deliverableAccount(), deliverableSpec(), integrations.DeliverableState{})
	if err != nil {
		t.Fatalf("EnsureDeliverable failed: %v", err)
	}

	want := integrations.DeliverableState{CampaignID: "camp_1", AdSetID: "adset_1", CreativeID: "cr_1", AdID: "ad_1"}
	if state != want {
		t.Errorf("state = %+v, want %+v", state, want)
	}
	for _, edge := range []string{"campaigns", "adsets", "adcreatives", "ads"} {
		if hits[edge] != 1 {
			t.Errorf("%s hit %d times, want 1", edge, hits[edge])
		}
	}
	if adsetForm["campaign_id"] != "camp_1" {
		t.Errorf("ad set campaign_id = %q", adsetForm["campaign_id"])
	}
	if adsetForm["optimization_goal"] != "OFFSITE_CONVERSIONS" {
		t.Errorf("ad set optimization_goal = %q", adsetForm["optimization_goal"])
	}
	if adsetForm["status"] != "PAUSED" {
		t.Errorf("ad set status = %q", adsetForm["status"])
	}
	if !strings.Contains(adsetForm["targeting"], "geo_locations") || !strings.Contains(adsetForm["targeting"], "NG") {
		t.Errorf("ad set targeting = %q", adsetForm["targeting"])
	}
	if !strings.Contains(adsetForm["promoted_object"], "pixel_1") || !strings.Contains(adsetForm["promoted_object"], "PURCHASE") {
		t.Errorf("ad set promoted_object = %q", adsetForm["promoted_object"])
	}
}

func TestEnsureDeliverableResumes(t *testing.T) {
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/campaigns"):
			t.Error("should not re-create campaign")
		case strings.HasSuffix(r.URL.Path, "/adsets"):
			t.Error("should not re-create ad set")
		case strings.HasSuffix(r.URL.Path, "/adcreatives"):
			hits["adcreatives"]++
			w.Write([]byte(`{"id":"cr_9"}`))
		case strings.HasSuffix(r.URL.Path, "/ads"):
			hits["ads"]++
			w.Write([]byte(`{"id":"ad_9"}`))
		}
	}))
	defer srv.Close()

	have := integrations.DeliverableState{CampaignID: "existing_camp", AdSetID: "existing_adset"}
	state, err := newTestClient(srv.URL).EnsureDeliverable(context.Background(), "tok", deliverableAccount(), deliverableSpec(), have)
	if err != nil {
		t.Fatalf("EnsureDeliverable failed: %v", err)
	}
	want := integrations.DeliverableState{CampaignID: "existing_camp", AdSetID: "existing_adset", CreativeID: "cr_9", AdID: "ad_9"}
	if state != want {
		t.Errorf("state = %+v, want %+v", state, want)
	}
}

func TestEnsureDeliverablePartialFailureReturnsState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/campaigns"):
			w.Write([]byte(`{"id":"camp_1"}`))
		case strings.HasSuffix(r.URL.Path, "/adsets"):
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"message":"bad ad set","code":100}}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	state, err := newTestClient(srv.URL).EnsureDeliverable(context.Background(), "tok", deliverableAccount(), deliverableSpec(), integrations.DeliverableState{})
	if err == nil {
		t.Fatal("expected error when ad set creation fails")
	}
	if state.CampaignID != "camp_1" {
		t.Errorf("partial state should keep campaign id, got %q", state.CampaignID)
	}
	if state.AdSetID != "" {
		t.Errorf("ad set id should be empty on failure, got %q", state.AdSetID)
	}
}

func TestFetchPagesAndPixels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/me/accounts"):
			w.Write([]byte(`{"data":[{"id":"p1","name":"Page One"}]}`))
		case strings.HasSuffix(r.URL.Path, "/adspixels"):
			w.Write([]byte(`{"data":[{"id":"px1","name":"Pixel One"}]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	pages, err := c.FetchPages(context.Background(), "tok")
	if err != nil || len(pages) != 1 || pages[0].ID != "p1" || pages[0].Name != "Page One" {
		t.Fatalf("FetchPages = %+v, err %v", pages, err)
	}
	pixels, err := c.FetchPixels(context.Background(), "tok", "123")
	if err != nil || len(pixels) != 1 || pixels[0].ID != "px1" {
		t.Fatalf("FetchPixels = %+v, err %v", pixels, err)
	}
}

func TestMetaTargeting(t *testing.T) {
	if got := metaTargeting(nil); !strings.Contains(got, `"countries":["US"]`) {
		t.Errorf("default targeting = %q", got)
	}
	if got := metaTargeting(map[string]any{"geo": []any{"NG", "GH"}}); !strings.Contains(got, "NG") || !strings.Contains(got, "GH") {
		t.Errorf("geo targeting = %q", got)
	}
	passthrough := map[string]any{"geo_locations": map[string]any{"countries": []any{"KE"}}}
	if got := metaTargeting(passthrough); !strings.Contains(got, "KE") {
		t.Errorf("passthrough targeting = %q", got)
	}
}

func TestMetaOptimization(t *testing.T) {
	opt, bill, event := metaOptimization(integrations.ObjectiveSales)
	if opt != "OFFSITE_CONVERSIONS" || bill != "IMPRESSIONS" || event != "PURCHASE" {
		t.Errorf("sales optimization = %q %q %q", opt, bill, event)
	}
	opt, _, event = metaOptimization(integrations.ObjectiveTraffic)
	if opt != "LINK_CLICKS" || event != "" {
		t.Errorf("traffic optimization = %q event=%q", opt, event)
	}
}

func TestMinorUnits(t *testing.T) {
	if got := minorUnits(50.50); got != "5050" {
		t.Errorf("minorUnits(50.50) = %q, want 5050", got)
	}
}
