package service

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

func validInput() CreateCampaignInput {
	return CreateCampaignInput{
		Name:         "Summer Launch",
		Objective:    "sales",
		DailyBudget:  100,
		Currency:     "NGN",
		AdAccountIDs: []string{"11111111-1111-1111-1111-111111111111"},
		Creative: &integrations.CreativeSpec{
			PrimaryText: "Shop our summer sale",
			LinkURL:     "https://example.com/sale",
			ImageURL:    "https://example.com/img.jpg",
		},
	}
}

func TestValidateCreateInput(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*CreateCampaignInput)
		wantErr bool
	}{
		{"valid", func(in *CreateCampaignInput) {}, false},
		{"empty name", func(in *CreateCampaignInput) { in.Name = "   " }, true},
		{"name too long", func(in *CreateCampaignInput) { in.Name = string(make([]byte, 201)) }, true},
		{"bad objective", func(in *CreateCampaignInput) { in.Objective = "growth" }, true},
		{"budget too low", func(in *CreateCampaignInput) { in.DailyBudget = 0 }, true},
		{"budget too high", func(in *CreateCampaignInput) { in.DailyBudget = MaxDailyBudget + 1 }, true},
		{"no ad accounts", func(in *CreateCampaignInput) { in.AdAccountIDs = nil }, true},
		{"invalid ad account id", func(in *CreateCampaignInput) { in.AdAccountIDs = []string{"not-a-uuid"} }, true},
		{"end before start", func(in *CreateCampaignInput) {
			in.StartDate = time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
			in.EndDate = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
		}, true},
		{"valid date range", func(in *CreateCampaignInput) {
			in.StartDate = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
			in.EndDate = time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
		}, false},
		{"missing creative", func(in *CreateCampaignInput) { in.Creative = nil }, true},
		{"bad link url", func(in *CreateCampaignInput) { in.Creative.LinkURL = "example.com" }, true},
		{"missing image", func(in *CreateCampaignInput) { in.Creative.ImageURL = "" }, true},
		{"missing copy", func(in *CreateCampaignInput) {
			in.Creative.PrimaryText = ""
			in.Creative.Headline = ""
		}, true},
		{"variants valid", func(in *CreateCampaignInput) {
			in.Creative = nil
			in.Variants = []integrations.VariantSpec{{
				Targeting: map[string]any{"geo": []string{"NG"}},
				Creative: &integrations.CreativeSpec{
					PrimaryText: "Test", LinkURL: "https://example.com", ImageURL: "https://example.com/img.jpg",
				},
			}}
		}, false},
		{"variants missing creative", func(in *CreateCampaignInput) {
			in.Creative = nil
			in.Variants = []integrations.VariantSpec{{Targeting: map[string]any{}, Creative: nil}}
		}, true},
		{"bad bid strategy", func(in *CreateCampaignInput) { in.BidStrategy = "INVALID" }, true},
		{"valid bid strategy", func(in *CreateCampaignInput) { in.BidStrategy = "COST_CAP" }, false},
		{"bad pacing type", func(in *CreateCampaignInput) { in.PacingType = "rocket" }, true},
		{"valid pacing type", func(in *CreateCampaignInput) { in.PacingType = "accelerated" }, false},
		{"freq cap without unit", func(in *CreateCampaignInput) { in.FrequencyCap = 5 }, true},
		{"freq cap with unit", func(in *CreateCampaignInput) {
			in.FrequencyCap = 5
			in.FrequencyCapUnit = "day"
		}, false},
		{"bad freq unit", func(in *CreateCampaignInput) {
			in.FrequencyCap = 5
			in.FrequencyCapUnit = "month"
		}, true},
		{"platforms instead of ad accounts", func(in *CreateCampaignInput) {
			in.AdAccountIDs = nil
			in.Platforms = []string{"meta"}
		}, false},
		{"neither ad accounts nor platforms", func(in *CreateCampaignInput) {
			in.AdAccountIDs = nil
			in.Platforms = nil
		}, true},
		{"invalid creative format", func(in *CreateCampaignInput) {
			in.Creative.Format = "hologram"
		}, true},
		{"valid creative format", func(in *CreateCampaignInput) {
			in.Creative.Format = "video"
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validInput()
			tt.mutate(&in)
			err := ValidateCreateInput(in)
			if tt.wantErr != (err != nil) {
				t.Errorf("ValidateCreateInput err=%v, wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				if _, ok := err.(ValidationError); !ok {
					t.Errorf("expected ValidationError, got %T", err)
				}
			}
		})
	}
}

func TestPlaceholderID(t *testing.T) {
	id := newPlaceholderID()
	if !isPlaceholderID(id) {
		t.Errorf("newPlaceholderID() %q should be a placeholder", id)
	}
	if isPlaceholderID("23847000111") {
		t.Error("a real platform id must not be detected as a placeholder")
	}
	if a, b := newPlaceholderID(), newPlaceholderID(); a == b {
		t.Error("placeholder ids should be unique")
	}
}

func TestDedupe(t *testing.T) {
	got := dedupe([]string{"meta", " meta ", "google", "", "google", "tiktok"})
	want := []string{"meta", "google", "tiktok"}
	if len(got) != len(want) {
		t.Fatalf("dedupe = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dedupe[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMarshalTargeting(t *testing.T) {
	if marshalTargeting(nil) != nil {
		t.Error("nil targeting should marshal to nil")
	}
	if marshalTargeting(map[string]any{}) != nil {
		t.Error("empty targeting should marshal to nil")
	}
	b := marshalTargeting(map[string]any{"geo": []string{"NG"}, "age_min": 18})
	if b == nil {
		t.Fatal("expected non-nil JSON")
	}
	var round map[string]any
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("invalid JSON produced: %v", err)
	}
	if _, ok := round["geo"]; !ok {
		t.Errorf("expected geo key in %s", b)
	}
}

func TestNumericToFloat(t *testing.T) {
	n := numericFromFloat(12.5)
	f, ok := numericToFloat(n)
	if !ok || math.Abs(f-12.5) > 1e-9 {
		t.Errorf("numericToFloat round trip = (%v, %v), want 12.5", f, ok)
	}
	var invalid = numericFromFloat(0) // valid 0
	if _, ok := numericToFloat(invalid); !ok {
		t.Error("zero should still be a valid numeric")
	}
}

func TestMarshalProvenance(t *testing.T) {
	b := marshalProvenance(nil, "user")
	if b != nil {
		t.Errorf("nil input should produce nil, got %s", b)
	}
	b = marshalProvenance(map[string]string{}, "user")
	if b != nil {
		t.Errorf("empty map should produce nil, got %s", b)
	}
	b = marshalProvenance(map[string]string{"name": "", "objective": "oma"}, "user")
	if b == nil {
		t.Fatal("expected non-nil")
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["name"] != "user" || m["objective"] != "oma" {
		t.Errorf("expected defaults filled, got %v", m)
	}
}

func TestProvenanceForField(t *testing.T) {
	m := map[string]string{"daily_budget": "oma", "name": "user"}
	b, _ := json.Marshal(m)
	if v := ProvenanceForField(b, "daily_budget"); v != "oma" {
		t.Errorf("expected oma, got %s", v)
	}
	if v := ProvenanceForField(b, "name"); v != "user" {
		t.Errorf("expected user, got %s", v)
	}
	if v := ProvenanceForField(b, "missing"); v != "user" {
		t.Errorf("expected default user for missing field, got %s", v)
	}
	if v := ProvenanceForField(nil, "x"); v != "user" {
		t.Errorf("expected user for nil input, got %s", v)
	}
}

func TestInt32Ptr(t *testing.T) {
	if p := int32Ptr(0); p.Valid {
		t.Error("zero should produce invalid int4")
	}
	if p := int32Ptr(5); !p.Valid || p.Int32 != 5 {
		t.Errorf("expected valid 5, got %v", p)
	}
}
