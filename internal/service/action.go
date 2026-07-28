package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/integrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// Who proposed the action.
	ActorUser = "user"
	ActorOma  = "oma"

	// Action types (open-ended; modify/optimize plug in here later).
	ActionCreateCampaign = "create_campaign"
	ActionSetStatus      = "set_status"
	ActionUpdateBudget   = "update_budget"

	// Action lifecycle.
	ActionStatusProposed = "proposed"
	ActionStatusRejected = "rejected"
	ActionStatusExecuted = "executed"
	ActionStatusFailed   = "failed"
)

// ErrActionNotFound is returned when an action id is not in the workspace.
var ErrActionNotFound = errors.New("action not found")

// ActionService manages the propose -> review -> approve/reject queue. Approving
// an action runs it through the same CampaignService the manual UI uses.
type ActionService struct {
	queries   *db.Queries
	campaigns *CampaignService
}

func NewActionService(queries *db.Queries, campaigns *CampaignService) *ActionService {
	return &ActionService{queries: queries, campaigns: campaigns}
}

// CampaignProposalPayload is the human-reviewable spec stored on a proposal. It
// mirrors the create-campaign request shape (string dates) so the UI can render
// and edit it before approval.
type CampaignProposalPayload struct {
	Name         string                     `json:"name"`
	Objective    string                     `json:"objective"`
	DailyBudget  float64                    `json:"daily_budget"`
	Currency     string                     `json:"currency"`
	StartDate    string                     `json:"start_date"`
	EndDate      string                     `json:"end_date"`
	CTA          string                     `json:"cta"`
	AdAccountIDs []string                   `json:"ad_account_ids"`
	Targeting    map[string]any             `json:"targeting"`
	Creative     *integrations.CreativeSpec `json:"creative"`
	Rationale    map[string]string          `json:"rationale,omitempty"`
	Provenance   map[string]string          `json:"provenance,omitempty"`
}

func (p CampaignProposalPayload) toInput() (CreateCampaignInput, error) {
	start, err := parseProposalDate(p.StartDate)
	if err != nil {
		return CreateCampaignInput{}, ValidationError{"start_date must be in YYYY-MM-DD format"}
	}
	end, err := parseProposalDate(p.EndDate)
	if err != nil {
		return CreateCampaignInput{}, ValidationError{"end_date must be in YYYY-MM-DD format"}
	}
	return CreateCampaignInput{
		Name:         p.Name,
		Objective:    p.Objective,
		DailyBudget:  p.DailyBudget,
		Currency:     p.Currency,
		StartDate:    start,
		EndDate:      end,
		CTA:          p.CTA,
		AdAccountIDs: p.AdAccountIDs,
		Targeting:    p.Targeting,
		Creative:     p.Creative,
	}, nil
}

// ProposeCreateCampaign validates a proposed campaign and stores it as a pending
// action for the user to review. Nothing is executed yet.
func (s *ActionService) ProposeCreateCampaign(ctx context.Context, workspaceID uuid.UUID, actor string, payload CampaignProposalPayload) (db.CampaignAction, error) {
	input, err := payload.toInput()
	if err != nil {
		return db.CampaignAction{}, err
	}
	if err := ValidateCreateInput(input); err != nil {
		return db.CampaignAction{}, err
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return db.CampaignAction{}, fmt.Errorf("marshal payload: %w", err)
	}

	return s.queries.CreateCampaignAction(ctx, db.CreateCampaignActionParams{
		WorkspaceID: pgtype.UUID{Bytes: workspaceID, Valid: true},
		Actor:       actor,
		Type:        ActionCreateCampaign,
		Payload:     raw,
	})
}

// StatusChangePayload is the proposal payload for a set_status action.
type StatusChangePayload struct {
	CampaignID string `json:"campaign_id"`
	Status     string `json:"status"`
}

// BudgetChangePayload is the proposal payload for an update_budget action.
type BudgetChangePayload struct {
	CampaignID  string  `json:"campaign_id"`
	DailyBudget float64 `json:"daily_budget"`
}

// ProposeStatusChange queues a pause/resume for review.
func (s *ActionService) ProposeStatusChange(ctx context.Context, workspaceID uuid.UUID, actor string, campaignID uuid.UUID, status string) (db.CampaignAction, error) {
	if status != StatusActive && status != StatusPaused {
		return db.CampaignAction{}, ValidationError{"status must be ACTIVE or PAUSED"}
	}
	raw, err := json.Marshal(StatusChangePayload{CampaignID: campaignID.String(), Status: status})
	if err != nil {
		return db.CampaignAction{}, fmt.Errorf("marshal payload: %w", err)
	}
	return s.queries.CreateCampaignAction(ctx, db.CreateCampaignActionParams{
		WorkspaceID: pgtype.UUID{Bytes: workspaceID, Valid: true},
		Actor:       actor,
		Type:        ActionSetStatus,
		Payload:     raw,
	})
}

// ProposeBudgetChange queues a budget change for review.
func (s *ActionService) ProposeBudgetChange(ctx context.Context, workspaceID uuid.UUID, actor string, campaignID uuid.UUID, dailyBudget float64) (db.CampaignAction, error) {
	if dailyBudget < MinDailyBudget || dailyBudget > MaxDailyBudget {
		return db.CampaignAction{}, ValidationError{fmt.Sprintf("daily_budget must be between %.0f and %.0f", MinDailyBudget, MaxDailyBudget)}
	}
	raw, err := json.Marshal(BudgetChangePayload{CampaignID: campaignID.String(), DailyBudget: dailyBudget})
	if err != nil {
		return db.CampaignAction{}, fmt.Errorf("marshal payload: %w", err)
	}
	return s.queries.CreateCampaignAction(ctx, db.CreateCampaignActionParams{
		WorkspaceID: pgtype.UUID{Bytes: workspaceID, Valid: true},
		Actor:       actor,
		Type:        ActionUpdateBudget,
		Payload:     raw,
	})
}

// ListActions returns the workspace's action queue, newest first.
func (s *ActionService) ListActions(ctx context.Context, workspaceID uuid.UUID) ([]db.CampaignAction, error) {
	return s.queries.ListCampaignActionsByWorkspace(ctx, pgtype.UUID{Bytes: workspaceID, Valid: true})
}

// ApproveAction executes a pending action via the shared CampaignService and
// records the outcome. The spec is re-validated at execution time (defense in
// depth — even though a human approved it).
func (s *ActionService) ApproveAction(ctx context.Context, workspaceID, actionID uuid.UUID) (db.CampaignAction, *CreateResult, error) {
	action, err := s.queries.GetCampaignActionByIDForWorkspace(ctx, db.GetCampaignActionByIDForWorkspaceParams{
		ID:          pgtype.UUID{Bytes: actionID, Valid: true},
		WorkspaceID: pgtype.UUID{Bytes: workspaceID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.CampaignAction{}, nil, ErrActionNotFound
		}
		return db.CampaignAction{}, nil, fmt.Errorf("load action: %w", err)
	}
	if action.Status != ActionStatusProposed {
		return action, nil, ValidationError{"action is not pending approval"}
	}

	switch action.Type {
	case ActionCreateCampaign:
		var payload CampaignProposalPayload
		if err := json.Unmarshal(action.Payload, &payload); err != nil {
			return s.fail(ctx, action, fmt.Sprintf("invalid stored payload: %v", err))
		}
		input, err := payload.toInput()
		if err != nil {
			return s.fail(ctx, action, err.Error())
		}
		res, err := s.campaigns.CreateCampaign(ctx, workspaceID, input)
		if err != nil {
			return s.fail(ctx, action, err.Error())
		}
		updated, uerr := s.queries.UpdateCampaignActionResult(ctx, db.UpdateCampaignActionResultParams{
			ID:     action.ID,
			Status: ActionStatusExecuted,
			Result: marshalActionResult(res),
		})
		if uerr != nil {
			return db.CampaignAction{}, &res, fmt.Errorf("persist action result: %w", uerr)
		}
		return updated, &res, nil

	case ActionSetStatus:
		var payload StatusChangePayload
		if err := json.Unmarshal(action.Payload, &payload); err != nil {
			return s.fail(ctx, action, fmt.Sprintf("invalid stored payload: %v", err))
		}
		cid, err := uuid.Parse(payload.CampaignID)
		if err != nil {
			return s.fail(ctx, action, "invalid campaign id in payload")
		}
		camp, platform, err := s.campaigns.SetCampaignStatus(ctx, workspaceID, cid, payload.Status)
		if err != nil {
			return s.fail(ctx, action, err.Error())
		}
		updated, uerr := s.queries.UpdateCampaignActionResult(ctx, db.UpdateCampaignActionResultParams{
			ID:     action.ID,
			Status: ActionStatusExecuted,
			Result: marshalCampaignResult(camp, platform),
		})
		if uerr != nil {
			return db.CampaignAction{}, nil, fmt.Errorf("persist action result: %w", uerr)
		}
		return updated, nil, nil

	case ActionUpdateBudget:
		var payload BudgetChangePayload
		if err := json.Unmarshal(action.Payload, &payload); err != nil {
			return s.fail(ctx, action, fmt.Sprintf("invalid stored payload: %v", err))
		}
		cid, err := uuid.Parse(payload.CampaignID)
		if err != nil {
			return s.fail(ctx, action, "invalid campaign id in payload")
		}
		camp, platform, err := s.campaigns.UpdateCampaignBudget(ctx, workspaceID, cid, payload.DailyBudget)
		if err != nil {
			return s.fail(ctx, action, err.Error())
		}
		updated, uerr := s.queries.UpdateCampaignActionResult(ctx, db.UpdateCampaignActionResultParams{
			ID:     action.ID,
			Status: ActionStatusExecuted,
			Result: marshalCampaignResult(camp, platform),
		})
		if uerr != nil {
			return db.CampaignAction{}, nil, fmt.Errorf("persist action result: %w", uerr)
		}
		return updated, nil, nil

	default:
		return action, nil, ValidationError{"unsupported action type: " + action.Type}
	}
}

// RejectAction marks a pending action as rejected.
func (s *ActionService) RejectAction(ctx context.Context, workspaceID, actionID uuid.UUID) (db.CampaignAction, error) {
	action, err := s.queries.GetCampaignActionByIDForWorkspace(ctx, db.GetCampaignActionByIDForWorkspaceParams{
		ID:          pgtype.UUID{Bytes: actionID, Valid: true},
		WorkspaceID: pgtype.UUID{Bytes: workspaceID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.CampaignAction{}, ErrActionNotFound
		}
		return db.CampaignAction{}, fmt.Errorf("load action: %w", err)
	}
	if action.Status != ActionStatusProposed {
		return action, ValidationError{"action is not pending approval"}
	}
	return s.queries.UpdateCampaignActionResult(ctx, db.UpdateCampaignActionResultParams{
		ID:     action.ID,
		Status: ActionStatusRejected,
	})
}

func (s *ActionService) fail(ctx context.Context, action db.CampaignAction, msg string) (db.CampaignAction, *CreateResult, error) {
	updated, err := s.queries.UpdateCampaignActionResult(ctx, db.UpdateCampaignActionResultParams{
		ID:     action.ID,
		Status: ActionStatusFailed,
		Error:  pgtype.Text{String: msg, Valid: true},
	})
	if err != nil {
		return action, nil, fmt.Errorf("persist action failure: %w", err)
	}
	return updated, nil, errors.New(msg)
}

func marshalActionResult(res CreateResult) []byte {
	type item struct {
		ID                 string `json:"id"`
		Platform           string `json:"platform"`
		Status             string `json:"status"`
		ExternalCampaignID string `json:"external_campaign_id"`
	}
	out := struct {
		Campaigns []item   `json:"campaigns"`
		Warnings  []string `json:"warnings"`
	}{Warnings: res.Warnings}
	for _, c := range res.Created {
		out.Campaigns = append(out.Campaigns, item{
			ID:                 formatUUID(c.Campaign.ID),
			Platform:           c.Platform,
			Status:             c.Campaign.Status,
			ExternalCampaignID: c.Campaign.ExternalCampaignID,
		})
	}
	b, _ := json.Marshal(out)
	return b
}

func marshalCampaignResult(camp db.Campaign, platform string) []byte {
	out := map[string]any{
		"id":                   formatUUID(camp.ID),
		"platform":             platform,
		"status":               camp.Status,
		"external_campaign_id": camp.ExternalCampaignID,
	}
	if f, ok := numericToFloat(camp.DailyBudget); ok {
		out["daily_budget"] = f
	}
	b, _ := json.Marshal(out)
	return b
}

func parseProposalDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse("2006-01-02", s)
}
