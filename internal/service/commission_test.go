package service

import "testing"

func TestCommissionMinorFor(t *testing.T) {
	cases := []struct {
		name string
		base int64
		rate int32
		want int64
	}{
		{"zero base", 0, 1000, 0},
		{"10 percent of 10.00", 1000, 1000, 100},
		{"10 percent of 50.00", 5000, 1000, 500},
		{"half minor rounds up", 5, 1000, 1},
		{"sub-half rounds down", 4, 1000, 0},
		{"15 percent", 10000, 1500, 1500},
		{"zero rate", 1000, 0, 0},
	}
	for _, tc := range cases {
		if got := commissionMinorFor(tc.base, tc.rate); got != tc.want {
			t.Errorf("%s: commissionMinorFor(%d, %d) = %d, want %d", tc.name, tc.base, tc.rate, got, tc.want)
		}
	}
}

func TestNormalizeCurrency(t *testing.T) {
	for _, ok := range []string{"usd", "NGN", " Usd "} {
		if _, err := normalizeCurrency(ok); err != nil {
			t.Errorf("normalizeCurrency(%q) unexpected error: %v", ok, err)
		}
	}
	if _, err := normalizeCurrency("EUR"); err == nil {
		t.Error("normalizeCurrency(EUR) expected error")
	}
}

func TestTotalBalanceMinor(t *testing.T) {
	s := WalletSummary{Balances: []CurrencyBalance{
		{Currency: CurrencyUSD, BalanceMinor: 0},
		{Currency: CurrencyNGN, BalanceMinor: 500},
	}}
	if got := totalBalanceMinor(s); got != 500 {
		t.Errorf("totalBalanceMinor = %d, want 500", got)
	}
	empty := WalletSummary{Balances: []CurrencyBalance{{Currency: CurrencyUSD, BalanceMinor: 0}}}
	if got := totalBalanceMinor(empty); got != 0 {
		t.Errorf("totalBalanceMinor empty = %d, want 0", got)
	}
}
