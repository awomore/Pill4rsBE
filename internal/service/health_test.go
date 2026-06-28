package service

import "testing"

func TestAssessSignals(t *testing.T) {
	tests := []struct {
		name          string
		sig           healthSignals
		budget        float64
		wantStatus    string
		wantAction    string // "" = none
		wantBudgetTo  float64
		wantSetStatus string
	}{
		{
			name:       "no data",
			sig:        healthSignals{Days: 0},
			wantStatus: HealthNoData,
		},
		{
			name:       "no spend",
			sig:        healthSignals{Days: 3, Spend: 0},
			wantStatus: HealthNoData,
		},
		{
			name:         "strong roas scales budget",
			sig:          healthSignals{Days: 7, Spend: 1000, Roas: 4.0, CTR: 2.0},
			budget:       5000,
			wantStatus:   HealthHealthy,
			wantAction:   ActionUpdateBudget,
			wantBudgetTo: 6000,
		},
		{
			name:       "strong roas but no budget set -> no action",
			sig:        healthSignals{Days: 7, Spend: 1000, Roas: 4.0, CTR: 2.0},
			budget:     0,
			wantStatus: HealthHealthy,
		},
		{
			name:          "below break-even pauses",
			sig:           healthSignals{Days: 7, Spend: 1000, Roas: 0.5, CTR: 1.0},
			budget:        5000,
			wantStatus:    HealthAtRisk,
			wantAction:    ActionSetStatus,
			wantSetStatus: StatusPaused,
		},
		{
			name:       "low ctr is a watch with no auto action",
			sig:        healthSignals{Days: 7, Spend: 1000, Roas: 1.5, CTR: 0.2},
			budget:     5000,
			wantStatus: HealthWatch,
		},
		{
			name:       "high frequency is fatigue watch",
			sig:        healthSignals{Days: 7, Spend: 1000, Roas: 1.5, CTR: 2.0, Frequency: 6},
			budget:     5000,
			wantStatus: HealthWatch,
		},
		{
			name:       "steady healthy",
			sig:        healthSignals{Days: 7, Spend: 1000, Roas: 1.5, CTR: 2.0, Frequency: 2},
			budget:     5000,
			wantStatus: HealthHealthy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := assessSignals(tt.sig, tt.budget)
			if got.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, tt.wantStatus)
			}
			if tt.wantAction == "" {
				if got.Action != nil {
					t.Errorf("expected no action, got %+v", got.Action)
				}
				return
			}
			if got.Action == nil {
				t.Fatalf("expected action %q, got none", tt.wantAction)
			}
			if got.Action.Type != tt.wantAction {
				t.Errorf("action type = %q, want %q", got.Action.Type, tt.wantAction)
			}
			if tt.wantBudgetTo != 0 && got.Action.DailyBudget != tt.wantBudgetTo {
				t.Errorf("action budget = %v, want %v", got.Action.DailyBudget, tt.wantBudgetTo)
			}
			if tt.wantSetStatus != "" && got.Action.Status != tt.wantSetStatus {
				t.Errorf("action status = %q, want %q", got.Action.Status, tt.wantSetStatus)
			}
			if got.What == "" || got.Why == "" || got.Recommendation == "" {
				t.Error("assessment must fill what/why/recommendation")
			}
		})
	}
}
