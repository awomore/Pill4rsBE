package service

import (
	"context"
	"log/slog"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Workspace spend states. A workspace whose wallet is empty is hard-paused:
// active campaigns are paused on the platform and new spend is refused.
const (
	SpendStateActive           = "active"
	SpendStatePausedLowBalance = "paused_low_balance"
)

// SpendGuard enforces the prepaid model: no balance, no spend. It is the single
// place that decides whether a workspace may create, launch, or resume spend.
type SpendGuard struct {
	queries   *db.Queries
	wallet    *WalletService
	campaigns *CampaignService
}

func NewSpendGuard(queries *db.Queries, wallet *WalletService, campaigns *CampaignService) *SpendGuard {
	return &SpendGuard{queries: queries, wallet: wallet, campaigns: campaigns}
}

// CanSpend returns a ValidationError when the workspace is hard-paused or has no
// available balance. Callers pass the error straight to the user.
func (g *SpendGuard) CanSpend(ctx context.Context, workspaceID uuid.UUID) error {
	ws, err := g.queries.GetWorkspaceByID(ctx, pgUUID(workspaceID))
	if err != nil {
		return nil
	}
	if ws.SpendState == SpendStatePausedLowBalance {
		return ValidationError{"your wallet balance is empty — top up to run campaigns"}
	}
	summary, err := g.wallet.Summary(ctx, workspaceID)
	if err != nil {
		return nil
	}
	if totalBalanceMinor(summary) <= 0 {
		return ValidationError{"your wallet balance is empty — top up to run campaigns"}
	}
	return nil
}

// Enforce checks the balance and either clears the pause (funds available) or
// hard-pauses every active campaign in the workspace. Safe to call repeatedly.
func (g *SpendGuard) Enforce(ctx context.Context, workspaceID uuid.UUID) {
	summary, err := g.wallet.Summary(ctx, workspaceID)
	if err != nil {
		slog.Error("spend guard: load wallet failed", "workspace", workspaceID, "error", err)
		return
	}
	if totalBalanceMinor(summary) > 0 {
		g.Clear(ctx, workspaceID)
		return
	}

	ws, err := g.queries.GetWorkspaceByID(ctx, pgUUID(workspaceID))
	if err == nil && ws.SpendState != SpendStatePausedLowBalance {
		if _, err := g.queries.SetWorkspaceSpendState(ctx, db.SetWorkspaceSpendStateParams{
			ID:               pgUUID(workspaceID),
			SpendState:       SpendStatePausedLowBalance,
			SpendStateReason: pgtype.Text{String: "wallet balance exhausted", Valid: true},
		}); err != nil {
			slog.Error("spend guard: set paused state failed", "workspace", workspaceID, "error", err)
		}
	}

	paused, err := g.campaigns.PauseWorkspaceCampaigns(ctx, workspaceID, "wallet balance exhausted")
	if err != nil {
		slog.Error("spend guard: pause campaigns failed", "workspace", workspaceID, "error", err)
		return
	}
	if paused > 0 {
		slog.Warn("spend guard: hard-paused workspace", "workspace", workspaceID, "campaigns", paused)
	}
}

// Clear lifts the hard pause when the workspace is funded again.
func (g *SpendGuard) Clear(ctx context.Context, workspaceID uuid.UUID) {
	ws, err := g.queries.GetWorkspaceByID(ctx, pgUUID(workspaceID))
	if err != nil || ws.SpendState == SpendStateActive {
		return
	}
	if _, err := g.queries.SetWorkspaceSpendState(ctx, db.SetWorkspaceSpendStateParams{
		ID:               pgUUID(workspaceID),
		SpendState:       SpendStateActive,
		SpendStateReason: pgtype.Text{},
	}); err != nil {
		slog.Error("spend guard: clear state failed", "workspace", workspaceID, "error", err)
	}
}

func totalBalanceMinor(s WalletSummary) int64 {
	var total int64
	for _, b := range s.Balances {
		if b.BalanceMinor > 0 {
			total += b.BalanceMinor
		}
	}
	return total
}
