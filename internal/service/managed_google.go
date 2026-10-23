package service

import (
	"context"
	"log/slog"

	"github.com/awomore/Pill4rsBE/internal/config"
	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/integrations/google"
	"github.com/google/uuid"
)

// ManagedGoogleService keeps managed Google Ads account budgets aligned with
// lifetime wallet top-ups.
type ManagedGoogleService struct {
	queries *db.Queries
	google  *google.Client
	cfg     *config.Config
	wallet  *WalletService
}

func NewManagedGoogleService(queries *db.Queries, googleClient *google.Client, cfg *config.Config, wallet *WalletService) *ManagedGoogleService {
	return &ManagedGoogleService{queries: queries, google: googleClient, cfg: cfg, wallet: wallet}
}

// SyncSpendCaps proposes account budgets for every managed Google account.
func (s *ManagedGoogleService) SyncSpendCaps(ctx context.Context, workspaceID uuid.UUID) {
	accounts, err := s.queries.GetAdAccountsByWorkspace(ctx, pgUUID(workspaceID))
	if err != nil {
		slog.Error("managed google: list accounts failed", "workspace", workspaceID, "error", err)
		return
	}
	for _, acct := range accounts {
		if acct.Platform != "google" || acct.BillingMode != "managed" {
			continue
		}
		_ = s.syncAccount(ctx, workspaceID, acct)
	}
}

// SetSpendCapForAccount proposes a budget for a single managed Google account.
func (s *ManagedGoogleService) SetSpendCapForAccount(ctx context.Context, workspaceID uuid.UUID, account db.AdAccount) error {
	return s.syncAccount(ctx, workspaceID, account)
}

func (s *ManagedGoogleService) syncAccount(ctx context.Context, workspaceID uuid.UUID, acct db.AdAccount) error {
	token, err := crypto.DecryptToken(acct.AccessTokenEncrypted, s.cfg.TokenEncryptionKey)
	if err != nil {
		slog.Error("managed google: decrypt token failed", "account", formatUUID(acct.ID), "error", err)
		return err
	}
	totalMinor, err := s.wallet.TotalTopUpsIn(ctx, workspaceID, acct.Currency, s.cfg.FXNGNPerUSD)
	if err != nil {
		slog.Error("managed google: total top-ups failed", "account", formatUUID(acct.ID), "error", err)
		return err
	}
	if totalMinor <= 0 {
		return nil
	}
	if _, err := s.google.SetAccountSpendCap(ctx, token, acct.ExternalAccountID, totalMinor); err != nil {
		slog.Error("managed google: account budget proposal failed", "account", formatUUID(acct.ID), "error", err)
		return err
	}
	return nil
}
