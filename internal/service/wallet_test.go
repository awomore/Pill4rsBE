package service

import (
	"testing"

	"github.com/awomore/Pill4rsBE/internal/db"
)

func TestCampaignHoldAmountMinor(t *testing.T) {
	if got := CampaignHoldAmountMinor(10); got != 2000 {
		t.Errorf("hold amount = %d, want 2000", got)
	}
	if got := CampaignHoldAmountMinor(0); got != 0 {
		t.Errorf("zero budget hold = %d", got)
	}
}

func TestUsesPrepaidWallet(t *testing.T) {
	managed := db.AdAccount{Platform: "meta", BillingMode: "managed"}
	byob := db.AdAccount{Platform: "meta", BillingMode: "byob"}
	dsp := db.AdAccount{Platform: "dv360", BillingMode: "managed"}
	if !usesPrepaidWallet(managed) {
		t.Error("managed account should use the wallet")
	}
	if usesPrepaidWallet(byob) {
		t.Error("byob account should not use the wallet")
	}
	if usesPrepaidWallet(dsp) {
		t.Error("track-only DSP should not use the wallet")
	}
}

func TestWalletCurrency(t *testing.T) {
	if got := walletCurrency("ngn"); got != CurrencyNGN {
		t.Errorf("walletCurrency(ngn) = %q", got)
	}
	if got := walletCurrency("EUR"); got != CurrencyUSD {
		t.Errorf("walletCurrency(EUR) = %q", got)
	}
}

func TestConvertTopUps(t *testing.T) {
	totals := map[string]int64{CurrencyUSD: 10000, CurrencyNGN: 150000}
	if got := convertTopUps(totals, CurrencyUSD, 1500); got != 10100 {
		t.Errorf("USD total = %d, want 10100", got)
	}
	if got := convertTopUps(totals, CurrencyNGN, 1500); got != 15150000 {
		t.Errorf("NGN total = %d, want 15150000", got)
	}
	if got := convertTopUps(totals, CurrencyUSD, 0); got != 10000 {
		t.Errorf("disabled FX USD = %d, want 10000", got)
	}
	if got := convertTopUps(totals, CurrencyNGN, 0); got != 150000 {
		t.Errorf("disabled FX NGN = %d, want 150000", got)
	}
}
