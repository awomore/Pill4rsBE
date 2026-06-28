package service

import (
	"testing"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

func validPayload() CampaignProposalPayload {
	return CampaignProposalPayload{
		Name:         "Summer Sale",
		Objective:    "sales",
		DailyBudget:  5000,
		Currency:     "NGN",
		StartDate:    "2026-07-01",
		EndDate:      "2026-07-31",
		CTA:          "SHOP_NOW",
		AdAccountIDs: []string{"11111111-1111-1111-1111-111111111111"},
		Targeting:    map[string]any{"geo": []any{"NG"}},
		Creative: &integrations.CreativeSpec{
			PrimaryText: "Big summer sale",
			Headline:    "50% off",
			LinkURL:     "https://shop.example.com",
			ImageURL:    "https://cdn.example.com/a.jpg",
		},
	}
}

func TestProposalPayloadToInput(t *testing.T) {
	in, err := validPayload().toInput()
	if err != nil {
		t.Fatalf("toInput failed: %v", err)
	}
	if in.Name != "Summer Sale" || in.Objective != "sales" || in.DailyBudget != 5000 {
		t.Errorf("unexpected input: %+v", in)
	}
	if in.StartDate.Format("2006-01-02") != "2026-07-01" {
		t.Errorf("start date: %v", in.StartDate)
	}
	if in.Creative == nil || in.Creative.LinkURL != "https://shop.example.com" {
		t.Errorf("creative: %+v", in.Creative)
	}
	// The mapped input must pass full validation.
	if err := ValidateCreateInput(in); err != nil {
		t.Errorf("valid payload should validate, got %v", err)
	}
}

func TestProposalPayloadBadDate(t *testing.T) {
	p := validPayload()
	p.StartDate = "07/01/2026"
	if _, err := p.toInput(); err == nil {
		t.Fatal("expected error for malformed start_date")
	}
}

func TestProposalPayloadMissingCreativeFailsValidation(t *testing.T) {
	p := validPayload()
	p.Creative = nil
	in, err := p.toInput()
	if err != nil {
		t.Fatalf("toInput failed: %v", err)
	}
	if err := ValidateCreateInput(in); err == nil {
		t.Fatal("expected validation failure when creative is missing")
	}
}
