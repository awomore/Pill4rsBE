package handler

import "testing"

func TestRangeToDays(t *testing.T) {
	tests := []struct {
		in       string
		wantDays int
		wantOK   bool
	}{
		{"", 7, true},
		{"7d", 7, true},
		{"30d", 30, true},
		{"90d", 90, true},
		{"1d", 0, false},
		{"7", 0, false},
		{"month", 0, false},
		{"7D", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			days, ok := rangeToDays(tt.in)
			if days != tt.wantDays || ok != tt.wantOK {
				t.Errorf("rangeToDays(%q) = (%d, %v), want (%d, %v)", tt.in, days, ok, tt.wantDays, tt.wantOK)
			}
		})
	}
}
