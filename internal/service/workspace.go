package service

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type WorkspaceService struct {
	queries *db.Queries
}

func NewWorkspaceService(queries *db.Queries) *WorkspaceService {
	return &WorkspaceService{queries: queries}
}

func (s *WorkspaceService) GetWorkspace(ctx context.Context, userID uuid.UUID) (db.Workspace, error) {
	pgID := pgtype.UUID{Bytes: userID, Valid: true}
	return s.queries.GetWorkspaceByUserID(ctx, pgID)
}

type UpdateWorkspaceInput struct {
	BusinessName   *string
	Industry       *string
	MonthlyBudget  *string
	PrimaryGoal    *string
	TargetAudience *string
}

func (s *WorkspaceService) UpdateWorkspace(ctx context.Context, userID uuid.UUID, input UpdateWorkspaceInput) (db.Workspace, error) {
	pgID := pgtype.UUID{Bytes: userID, Valid: true}

	ws, err := s.queries.GetWorkspaceByUserID(ctx, pgID)
	if err != nil {
		return db.Workspace{}, fmt.Errorf("get workspace: %w", err)
	}

	params := db.UpdateWorkspaceProfileParams{
		ID:             ws.ID,
		BusinessName:   ws.BusinessName,
		Industry:       ws.Industry,
		MonthlyBudget:  ws.MonthlyBudget,
		PrimaryGoal:    ws.PrimaryGoal,
		TargetAudience: ws.TargetAudience,
	}

	if input.BusinessName != nil {
		params.BusinessName = pgtype.Text{String: *input.BusinessName, Valid: true}
	}
	if input.Industry != nil {
		params.Industry = pgtype.Text{String: *input.Industry, Valid: true}
	}
	if input.MonthlyBudget != nil {
		n := new(big.Int)
		if _, ok := n.SetString(strings.TrimSpace(*input.MonthlyBudget), 10); ok {
			params.MonthlyBudget = pgtype.Numeric{Int: n, Exp: 0, Valid: true}
		}
	}
	if input.PrimaryGoal != nil {
		params.PrimaryGoal = pgtype.Text{String: *input.PrimaryGoal, Valid: true}
	}
	if input.TargetAudience != nil {
		params.TargetAudience = pgtype.Text{String: *input.TargetAudience, Valid: true}
	}

	return s.queries.UpdateWorkspaceProfile(ctx, params)
}
