package service

import (
	"context"
	"log/slog"

	"github.com/awomore/Pill4rsBE/internal/config"
	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/integrations/tiktok"
	"github.com/google/uuid"
)

// ManagedTikTokService keeps managed TikTok advertiser budgets aligned with
// lifetime wallet top-ups.
type ManagedTikTokService struct {
	queries *db.Queries
	tiktok  *tiktok.Client
	cfg     *config.Config
	wallet  *WalletService
}

func NewManagedTikTokService(queries *db.Queries, tiktokClient *tiktok.Client, cfg *config.Config, wallet *WalletService) *ManagedTikTokService {
	return &ManagedTikTokService{queries: queries, tiktok: tiktokClient, cfg: cfg, wallet: wallet}
}

// SyncBudgets updates every managed TikTok advertiser in a workspace.
func (s *ManagedTikTokService) SyncBudgets(ctx context.Context, workspaceID uuid.UUID) {
	accounts, err := s.queries.GetAdAccountsByWorkspace(ctx, pgUUID(workspaceID))
	if err != nil {
		slog.Error("managed tiktok: list accounts failed", "workspace", workspaceID, "error", err)
		return
	}
	for _, acct := range accounts {
		if acct.Platform != "tiktok" || acct.BillingMode != "managed" {
			continue
		}
		_ = s.syncAccount(ctx, workspaceID, acct)
	}
}

// SetBudgetForAccount refreshes a single managed TikTok advertiser.
func (s *ManagedTikTokService) SetBudgetForAccount(ctx context.Context, workspaceID uuid.UUID, account db.AdAccount) error {
	return s.syncAccount(ctx, workspaceID, account)
}

func (s *ManagedTikTokService) syncAccount(ctx context.Context, workspaceID uuid.UUID, acct db.AdAccount) error {
	token, err := crypto.DecryptToken(acct.AccessTokenEncrypted, s.cfg.TokenEncryptionKey)
	if err != nil {
		slog.Error("managed tiktok: decrypt token failed", "account", formatUUID(acct.ID), "error", err)
		return err
	}
	totalMinor, err := s.wallet.TotalTopUpsIn(ctx, workspaceID, acct.Currency, s.cfg.FXNGNPerUSD)
	if err != nil {
		slog.Error("managed tiktok: total top-ups failed", "account", formatUUID(acct.ID), "error", err)
		return err
	}
	if totalMinor <= 0 {
		return nil
	}
	budget := float64(totalMinor) / 100
	if err := s.tiktok.SetAdvertiserBudget(ctx, token, acct.ExternalAccountID, budget, "BUDGET_MODE_TOTAL"); err != nil {
		slog.Error("managed tiktok: set budget failed", "account", formatUUID(acct.ID), "error", err)
		return err
	}
	return nil
}
