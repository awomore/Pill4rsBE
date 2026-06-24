package service

import (
	"math"
	"testing"
)

func TestNumericFromFloat(t *testing.T) {
	tests := []float64{0, 1234.56, 3.25, 0.0001, 9999999.99}
	for _, want := range tests {
		n := numericFromFloat(want)
		if !n.Valid {
			t.Fatalf("numericFromFloat(%v) produced invalid numeric", want)
		}
		f, err := n.Float64Value()
		if err != nil || !f.Valid {
			t.Fatalf("Float64Value failed for %v: %v", want, err)
		}
		if math.Abs(f.Float64-want) > 1e-6 {
			t.Errorf("round trip: got %v want %v", f.Float64, want)
		}
	}
}

func TestBudgetToNumeric(t *testing.T) {
	n := budgetToNumeric("1050")
	if !n.Valid {
		t.Fatal("expected valid numeric for \"1050\"")
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		t.Fatalf("Float64Value failed: %v", err)
	}
	if math.Abs(f.Float64-10.50) > 1e-9 {
		t.Errorf("minor->major conversion: got %v want 10.50", f.Float64)
	}

	if budgetToNumeric("").Valid {
		t.Error("empty budget should be NULL numeric")
	}
	if budgetToNumeric("not-a-number").Valid {
		t.Error("invalid budget should be NULL numeric")
	}
}

func TestTextOrNull(t *testing.T) {
	if textOrNull("").Valid {
		t.Error("empty string should be NULL text")
	}
	v := textOrNull("OUTCOME_SALES")
	if !v.Valid || v.String != "OUTCOME_SALES" {
		t.Errorf("unexpected text value: %+v", v)
	}
}
