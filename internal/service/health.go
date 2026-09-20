package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Health rule thresholds.
const (
	healthyRoas       = 3.0
	breakEvenRoas     = 1.0
	lowCTRPercent     = 0.5
	highFrequency     = 5.0
	scaleBudgetFactor = 1.2
	healthWindowDays  = 14
)

// Health statuses Oma assigns to a campaign.
const (
	HealthNoData  = "no_data"
	HealthHealthy = "healthy"
	HealthWatch   = "watch"
	HealthAtRisk  = "at_risk"
)

// RecommendedAction is a concrete, one-click-applicable fix.
type RecommendedAction struct {
	Type        string  `json:"type"` // set_status | update_budget
	Status      string  `json:"status,omitempty"`
	DailyBudget float64 `json:"daily_budget,omitempty"`
	Summary     string  `json:"summary"`
}

// HealthAssessment is the four-question card: what / why / what to do (+ a fix).
type HealthAssessment struct {
	Status         string             `json:"status"`
	What           string             `json:"what"`
	Why            string             `json:"why"`
	Recommendation string             `json:"recommendation"`
	Action         *RecommendedAction `json:"action,omitempty"`
}

type healthSignals struct {
	Days        int
	Spend       float64
	Revenue     float64
	Roas        float64
	CTR         float64 // percent
	CPC         float64
	Conversions int64
	Frequency   float64
}

// AssessHealth computes Oma's health card for a campaign from its recent snapshots.
func (s *CampaignService) AssessHealth(ctx context.Context, workspaceID, campaignID uuid.UUID) (HealthAssessment, db.Campaign, error) {
	camp, err := s.queries.GetCampaignByIDForWorkspace(ctx, db.GetCampaignByIDForWorkspaceParams{
		ID:          pgtype.UUID{Bytes: campaignID, Valid: true},
		WorkspaceID: pgtype.UUID{Bytes: workspaceID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return HealthAssessment{}, db.Campaign{}, ErrCampaignNotFound
		}
		return HealthAssessment{}, db.Campaign{}, fmt.Errorf("load campaign: %w", err)
	}

	end := time.Now().UTC()
	start := end.AddDate(0, 0, -healthWindowDays)
	snaps, err := s.queries.GetSnapshotsByCampaignAndDateRange(ctx, db.GetSnapshotsByCampaignAndDateRangeParams{
		CampaignID: camp.ID,
		Date:       pgtype.Date{Time: start, Valid: true},
		Date_2:     pgtype.Date{Time: end, Valid: true},
	})
	if err != nil {
		return HealthAssessment{}, db.Campaign{}, fmt.Errorf("load snapshots: %w", err)
	}

	budget, _ := numericToFloat(camp.DailyBudget)
	return assessSignals(computeSignals(snaps), budget), camp, nil
}

// assessCampaign runs the health rule engine for an already-loaded campaign.
// Shared by the single-campaign card and the workspace-wide review.
func (s *CampaignService) assessCampaign(ctx context.Context, camp db.Campaign) (HealthAssessment, healthSignals, error) {
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -healthWindowDays)
	snaps, err := s.queries.GetSnapshotsByCampaignAndDateRange(ctx, db.GetSnapshotsByCampaignAndDateRangeParams{
		CampaignID: camp.ID,
		Date:       pgtype.Date{Time: start, Valid: true},
		Date_2:     pgtype.Date{Time: end, Valid: true},
	})
	if err != nil {
		return HealthAssessment{}, healthSignals{}, fmt.Errorf("load snapshots: %w", err)
	}
	budget, _ := numericToFloat(camp.DailyBudget)
	sig := computeSignals(snaps)
	return assessSignals(sig, budget), sig, nil
}

func computeSignals(snaps []db.PerformanceSnapshot) healthSignals {
	var sig healthSignals
	var impressions, clicks, reach int64
	for _, sn := range snaps {
		sig.Days++
		spend, _ := numericToFloat(sn.Spend)
		sig.Spend += spend
		if roas, ok := numericToFloat(sn.Roas); ok {
			sig.Revenue += spend * roas
		}
		impressions += sn.Impressions
		clicks += sn.Clicks
		reach += sn.Reach
		sig.Conversions += sn.Conversions
	}
	if sig.Spend > 0 {
		sig.Roas = sig.Revenue / sig.Spend
	}
	if impressions > 0 {
		sig.CTR = float64(clicks) / float64(impressions) * 100
	}
	if clicks > 0 {
		sig.CPC = sig.Spend / float64(clicks)
	}
	if reach > 0 {
		sig.Frequency = float64(impressions) / float64(reach)
	}
	return sig
}

// assessSignals is the pure rule engine behind the health card.
func assessSignals(sig healthSignals, currentBudget float64) HealthAssessment {
	if sig.Days == 0 || sig.Spend == 0 {
		return HealthAssessment{
			Status:         HealthNoData,
			What:           "Not enough data yet.",
			Why:            "This campaign hasn't recorded any spend in the last 14 days.",
			Recommendation: "Launch it (or run a sync) and check back once it's delivering.",
		}
	}

	switch {
	case sig.Roas >= healthyRoas:
		a := HealthAssessment{
			Status:         HealthHealthy,
			What:           "This campaign is performing well.",
			Why:            fmt.Sprintf("It's returning %.1fx ROAS on %.0f spend over %d days.", sig.Roas, sig.Spend, sig.Days),
			Recommendation: "Increase the daily budget to capture more of this performance.",
		}
		if currentBudget > 0 {
			newBudget := currentBudget * scaleBudgetFactor
			if newBudget > MaxDailyBudget {
				newBudget = MaxDailyBudget
			}
			a.Action = &RecommendedAction{
				Type:        ActionUpdateBudget,
				DailyBudget: newBudget,
				Summary:     fmt.Sprintf("Increase daily budget by 20%% (to %.0f).", newBudget),
			}
		}
		return a

	case sig.Roas < breakEvenRoas:
		return HealthAssessment{
			Status:         HealthAtRisk,
			What:           "This campaign is spending more than it returns.",
			Why:            fmt.Sprintf("ROAS is %.2fx — below break-even — across %.0f spend.", sig.Roas, sig.Spend),
			Recommendation: "Pause it and revisit targeting or creative before spending more.",
			Action: &RecommendedAction{
				Type:    ActionSetStatus,
				Status:  StatusPaused,
				Summary: "Pause this campaign.",
			},
		}

	case sig.CTR < lowCTRPercent:
		return HealthAssessment{
			Status:         HealthWatch,
			What:           "Engagement is low.",
			Why:            fmt.Sprintf("Click-through rate is %.2f%%, which suggests the creative or audience isn't landing.", sig.CTR),
			Recommendation: "Refresh the creative or broaden the audience.",
		}

	case sig.Frequency > highFrequency:
		return HealthAssessment{
			Status:         HealthWatch,
			What:           "Audience fatigue is setting in.",
			Why:            fmt.Sprintf("People have seen these ads %.1f times on average.", sig.Frequency),
			Recommendation: "Refresh creative or expand the audience to bring frequency down.",
		}

	default:
		return HealthAssessment{
			Status:         HealthHealthy,
			What:           "This campaign is steady.",
			Why:            fmt.Sprintf("ROAS is %.2fx with a %.2f%% CTR.", sig.Roas, sig.CTR),
			Recommendation: "Keep monitoring — no changes needed right now.",
		}
	}
}
