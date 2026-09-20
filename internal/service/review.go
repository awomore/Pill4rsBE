package service

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// CampaignReview is one campaign's health within a workspace-wide review.
type CampaignReview struct {
	CampaignID uuid.UUID        `json:"-"`
	Campaign   string           `json:"campaign_id"`
	Name       string           `json:"name"`
	Platform   string           `json:"platform"`
	Status     string           `json:"status"`
	Spend      float64          `json:"spend"`
	Roas       float64          `json:"roas"`
	Health     HealthAssessment `json:"health"`
}

// ReviewSummary aggregates a review so the UI (and Oma) can see the picture at
// a glance.
type ReviewSummary struct {
	Total      int     `json:"total"`
	Healthy    int     `json:"healthy"`
	Watch      int     `json:"watch"`
	AtRisk     int     `json:"at_risk"`
	NoData     int     `json:"no_data"`
	Actionable int     `json:"actionable"`
	Spend      float64 `json:"spend"`
}

// WorkspaceReview is Oma's read of every campaign across every connected
// platform: a health card per campaign plus the aggregate.
type WorkspaceReview struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Summary     ReviewSummary    `json:"summary"`
	Campaigns   []CampaignReview `json:"campaigns"`
}

// ReviewWorkspace assesses every campaign in the workspace with the same rule
// engine used by the per-campaign health card, so Oma has one platform-agnostic
// view of all ads.
func (s *CampaignService) ReviewWorkspace(ctx context.Context, workspaceID uuid.UUID) (WorkspaceReview, error) {
	campaigns, err := s.queries.GetCampaignsByWorkspace(ctx, pgUUID(workspaceID))
	if err != nil {
		return WorkspaceReview{}, err
	}

	platformByAccount := map[string]string{}
	if accounts, err := s.queries.GetAdAccountsByWorkspace(ctx, pgUUID(workspaceID)); err == nil {
		for _, a := range accounts {
			platformByAccount[formatUUID(a.ID)] = a.Platform
		}
	}

	review := WorkspaceReview{
		GeneratedAt: time.Now().UTC(),
		Campaigns:   make([]CampaignReview, 0, len(campaigns)),
	}
	for _, camp := range campaigns {
		assessment, sig, err := s.assessCampaign(ctx, camp)
		if err != nil {
			continue
		}
		review.Campaigns = append(review.Campaigns, CampaignReview{
			CampaignID: uuidFromPG(camp.ID),
			Campaign:   formatUUID(camp.ID),
			Name:       camp.Name,
			Platform:   platformByAccount[formatUUID(camp.AdAccountID)],
			Status:     camp.Status,
			Spend:      sig.Spend,
			Roas:       sig.Roas,
			Health:     assessment,
		})

		review.Summary.Total++
		review.Summary.Spend += sig.Spend
		switch assessment.Status {
		case HealthHealthy:
			review.Summary.Healthy++
		case HealthWatch:
			review.Summary.Watch++
		case HealthAtRisk:
			review.Summary.AtRisk++
		case HealthNoData:
			review.Summary.NoData++
		}
		if assessment.Action != nil {
			review.Summary.Actionable++
		}
	}
	return review, nil
}
