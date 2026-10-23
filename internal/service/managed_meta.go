package service

import (
	"context"
	"log/slog"

	"github.com/awomore/Pill4rsBE/internal/config"
	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/integrations/meta"
	"github.com/google/uuid"
)

// ManagedMetaService keeps managed Meta ad-account spend caps in sync with the
// lifetime top-ups in the workspace wallet.
type ManagedMetaService struct {
	queries *db.Queries
	meta    *meta.Client
	cfg     *config.Config
	wallet  *WalletService
}

func NewManagedMetaService(queries *db.Queries, metaClient *meta.Client, cfg *config.Config, wallet *WalletService) *ManagedMetaService {
	return &ManagedMetaService{queries: queries, meta: metaClient, cfg: cfg, wallet: wallet}
}

// SyncSpendCaps updates every managed Meta account in a workspace. Called after
// a wallet credit (top-up/refund/adjustment).
func (s *ManagedMetaService) SyncSpendCaps(ctx context.Context, workspaceID uuid.UUID) {
	accounts, err := s.queries.GetAdAccountsByWorkspace(ctx, pgUUID(workspaceID))
	if err != nil {
		slog.Error("managed meta: list accounts failed", "workspace", workspaceID, "error", err)
		return
	}
	for _, acct := range accounts {
		if acct.Platform != "meta" || acct.BillingMode != "managed" {
			continue
		}
		s.syncAccount(ctx, workspaceID, acct)
	}
}

// SetSpendCapForAccount refreshes a single managed Meta account.
func (s *ManagedMetaService) SetSpendCapForAccount(ctx context.Context, workspaceID uuid.UUID, account db.AdAccount) error {
	return s.syncAccount(ctx, workspaceID, account)
}

func (s *ManagedMetaService) syncAccount(ctx context.Context, workspaceID uuid.UUID, acct db.AdAccount) error {
	token, err := crypto.DecryptToken(acct.AccessTokenEncrypted, s.cfg.TokenEncryptionKey)
	if err != nil {
		slog.Error("managed meta: decrypt token failed", "account", formatUUID(acct.ID), "error", err)
		return err
	}
	total, err := s.wallet.TotalTopUpsIn(ctx, workspaceID, acct.Currency, s.cfg.FXNGNPerUSD)
	if err != nil {
		slog.Error("managed meta: total top-ups failed", "account", formatUUID(acct.ID), "error", err)
		return err
	}
	if total <= 0 {
		return nil
	}
	if err := s.meta.SetSpendCap(ctx, token, acct.ExternalAccountID, total); err != nil {
		slog.Error("managed meta: set spend cap failed", "account", formatUUID(acct.ID), "error", err)
		return err
	}
	return nil
}
