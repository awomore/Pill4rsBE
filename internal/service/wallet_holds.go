package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Wallet hold lifecycle.
const (
	HoldStatusActive   = "active"
	HoldStatusReleased = "released"
	HoldStatusConsumed = "consumed"
)

// CampaignHoldDays is how many days of budget we reserve when a managed
// campaign is activated. Platforms can overspend a daily budget, so a small
// buffer protects the wallet.
const CampaignHoldDays = 2.0

// HoldRequest reserves funds for a specific source (e.g. a campaign).
type HoldRequest struct {
	WorkspaceID    uuid.UUID
	Currency       string
	AmountMinor    int64
	SourceType     string
	SourceID       string
	IdempotencyKey string
	Metadata       map[string]any
}

// Hold reserves funds by debiting the available wallet balance and recording a
// hold. It is idempotent per (wallet, idempotency_key).
func (s *WalletService) Hold(ctx context.Context, req HoldRequest) (db.WalletHold, error) {
	if req.AmountMinor <= 0 {
		return db.WalletHold{}, ValidationError{"hold amount must be positive"}
	}
	currency, err := normalizeCurrency(req.Currency)
	if err != nil {
		return db.WalletHold{}, err
	}
	if req.IdempotencyKey == "" {
		return db.WalletHold{}, fmt.Errorf("hold idempotency key is required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.WalletHold{}, fmt.Errorf("begin hold tx: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)

	w, err := q.EnsureWallet(ctx, pgUUID(req.WorkspaceID))
	if err != nil {
		return db.WalletHold{}, fmt.Errorf("ensure wallet: %w", err)
	}
	if _, err := q.EnsureWalletBalance(ctx, db.EnsureWalletBalanceParams{WalletID: w.ID, Currency: currency}); err != nil {
		return db.WalletHold{}, fmt.Errorf("ensure balance: %w", err)
	}

	existing, err := q.GetWalletHoldByIdempotency(ctx, db.GetWalletHoldByIdempotencyParams{
		WalletID:       w.ID,
		IdempotencyKey: req.IdempotencyKey,
	})
	var existingHold *db.WalletHold
	if err == nil {
		if existing.Status == HoldStatusActive {
			return existing, nil
		}
		existingHold = &existing
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return db.WalletHold{}, fmt.Errorf("check hold idempotency: %w", err)
	}

	bal, err := q.GetWalletBalanceForUpdate(ctx, db.GetWalletBalanceForUpdateParams{WalletID: w.ID, Currency: currency})
	if err != nil {
		return db.WalletHold{}, fmt.Errorf("load balance: %w", err)
	}
	if bal.BalanceMinor < req.AmountMinor {
		return db.WalletHold{}, ErrInsufficientBalance
	}

	updated, err := q.AddWalletBalance(ctx, db.AddWalletBalanceParams{
		WalletID:     w.ID,
		Currency:     currency,
		BalanceMinor: -req.AmountMinor,
	})
	if err != nil {
		return db.WalletHold{}, fmt.Errorf("reserve balance: %w", err)
	}

	var hold db.WalletHold
	ledgerKey := "hold:" + req.IdempotencyKey
	if existingHold != nil {
		hold, err = q.ReactivateWalletHold(ctx, db.ReactivateWalletHoldParams{
			ID:             existingHold.ID,
			RemainingMinor: req.AmountMinor,
		})
		ledgerKey = fmt.Sprintf("hold:%s:%d", req.IdempotencyKey, hold.Version)
	} else {
		hold, err = q.InsertWalletHold(ctx, db.InsertWalletHoldParams{
			WalletID:       w.ID,
			WorkspaceID:    pgUUID(req.WorkspaceID),
			Currency:       currency,
			AmountMinor:    req.AmountMinor,
			RemainingMinor: req.AmountMinor,
			Status:         HoldStatusActive,
			SourceType:     textOrNull(req.SourceType),
			SourceID:       textOrNull(req.SourceID),
			IdempotencyKey: req.IdempotencyKey,
		})
	}
	if err != nil {
		return db.WalletHold{}, fmt.Errorf("save hold: %w", err)
	}

	var meta []byte
	if len(req.Metadata) > 0 {
		meta, _ = json.Marshal(req.Metadata)
	}
	if _, err := q.InsertWalletTransaction(ctx, db.InsertWalletTransactionParams{
		WalletID:          w.ID,
		Currency:          currency,
		Kind:              TxKindHold,
		AmountMinor:       -req.AmountMinor,
		BalanceAfterMinor: updated.BalanceMinor,
		IdempotencyKey:    ledgerKey,
		SourceType:        textOrNull(req.SourceType),
		SourceID:          textOrNull(req.SourceID),
		Metadata:          meta,
	}); err != nil {
		return db.WalletHold{}, fmt.Errorf("insert hold ledger entry: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return db.WalletHold{}, fmt.Errorf("commit hold: %w", err)
	}
	return hold, nil
}

// ReleaseHoldBySource releases any remaining amount held for a source.
func (s *WalletService) ReleaseHoldBySource(ctx context.Context, workspaceID uuid.UUID, sourceType, sourceID string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin release tx: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)

	w, err := q.EnsureWallet(ctx, pgUUID(workspaceID))
	if err != nil {
		return 0, fmt.Errorf("ensure wallet: %w", err)
	}
	hold, err := q.GetActiveWalletHoldForSource(ctx, db.GetActiveWalletHoldForSourceParams{
		WalletID:   w.ID,
		SourceType: textOrNull(sourceType),
		SourceID:   textOrNull(sourceID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("load hold: %w", err)
	}

	key := fmt.Sprintf("release:hold:%s:%d", formatUUID(hold.ID), hold.Version)
	if existing, err := q.GetWalletTransactionByIdempotency(ctx, db.GetWalletTransactionByIdempotencyParams{
		WalletID: w.ID, IdempotencyKey: key,
	}); err == nil {
		// Already released; make sure the hold row reflects it.
		if hold.RemainingMinor != 0 || hold.Status == HoldStatusActive {
			_, _ = q.UpdateWalletHoldRemaining(ctx, db.UpdateWalletHoldRemainingParams{
				ID: hold.ID, RemainingMinor: 0, Status: HoldStatusReleased,
			})
		}
		return absInt64(existing.AmountMinor), tx.Commit(ctx)
	}

	bal, err := q.GetWalletBalance(ctx, db.GetWalletBalanceParams{WalletID: w.ID, Currency: hold.Currency})
	if err != nil {
		return 0, fmt.Errorf("load balance: %w", err)
	}
	updated, err := q.AddWalletBalance(ctx, db.AddWalletBalanceParams{
		WalletID:     w.ID,
		Currency:     hold.Currency,
		BalanceMinor: hold.RemainingMinor,
	})
	if err != nil {
		return 0, fmt.Errorf("release balance: %w", err)
	}
	released := hold.RemainingMinor
	if _, err := q.UpdateWalletHoldRemaining(ctx, db.UpdateWalletHoldRemainingParams{
		ID: hold.ID, RemainingMinor: 0, Status: HoldStatusReleased,
	}); err != nil {
		return 0, fmt.Errorf("close hold: %w", err)
	}
	if _, err := q.InsertWalletTransaction(ctx, db.InsertWalletTransactionParams{
		WalletID:          w.ID,
		Currency:          hold.Currency,
		Kind:              TxKindRelease,
		AmountMinor:       released,
		BalanceAfterMinor: updated.BalanceMinor,
		IdempotencyKey:    key,
		SourceType:        hold.SourceType,
		SourceID:          hold.SourceID,
		Metadata:          []byte(fmt.Sprintf(`{"previous_balance_minor":%d}`, bal.BalanceMinor)),
	}); err != nil {
		return 0, fmt.Errorf("insert release ledger entry: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit release: %w", err)
	}
	return released, nil
}

// ConsumeHoldForSpend releases held funds as real spend is recorded. It
// returns the amount covered by the hold. The caller then debits the full
// spend; the release + debit net out to the real spend.
func (s *WalletService) ConsumeHoldForSpend(ctx context.Context, workspaceID uuid.UUID, currency, sourceType, sourceID string, spendMinor int64, idempotencyKey string) (int64, error) {
	if spendMinor <= 0 {
		return 0, nil
	}
	currency, err := normalizeCurrency(currency)
	if err != nil {
		return 0, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin consume tx: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)

	w, err := q.EnsureWallet(ctx, pgUUID(workspaceID))
	if err != nil {
		return 0, fmt.Errorf("ensure wallet: %w", err)
	}

	key := "release:spend:" + idempotencyKey
	if existing, err := q.GetWalletTransactionByIdempotency(ctx, db.GetWalletTransactionByIdempotencyParams{
		WalletID: w.ID, IdempotencyKey: key,
	}); err == nil {
		return absInt64(existing.AmountMinor), tx.Commit(ctx)
	}

	hold, err := q.GetActiveWalletHoldForSource(ctx, db.GetActiveWalletHoldForSourceParams{
		WalletID:   w.ID,
		SourceType: textOrNull(sourceType),
		SourceID:   textOrNull(sourceID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("load hold: %w", err)
	}
	if hold.Currency != currency {
		return 0, fmt.Errorf("hold currency %s does not match spend currency %s", hold.Currency, currency)
	}

	covered := spendMinor
	if covered > hold.RemainingMinor {
		covered = hold.RemainingMinor
	}
	if covered <= 0 {
		return 0, nil
	}

	updated, err := q.AddWalletBalance(ctx, db.AddWalletBalanceParams{
		WalletID:     w.ID,
		Currency:     currency,
		BalanceMinor: covered,
	})
	if err != nil {
		return 0, fmt.Errorf("release covered hold: %w", err)
	}

	remaining := hold.RemainingMinor - covered
	status := HoldStatusActive
	if remaining == 0 {
		status = HoldStatusConsumed
	}
	if _, err := q.UpdateWalletHoldRemaining(ctx, db.UpdateWalletHoldRemainingParams{
		ID: hold.ID, RemainingMinor: remaining, Status: status,
	}); err != nil {
		return 0, fmt.Errorf("update hold: %w", err)
	}
	if _, err := q.InsertWalletTransaction(ctx, db.InsertWalletTransactionParams{
		WalletID:          w.ID,
		Currency:          currency,
		Kind:              TxKindRelease,
		AmountMinor:       covered,
		BalanceAfterMinor: updated.BalanceMinor,
		IdempotencyKey:    key,
		SourceType:        textOrNull("snapshot"),
		SourceID:          textOrNull(idempotencyKey),
	}); err != nil {
		return 0, fmt.Errorf("insert spend release: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit consume: %w", err)
	}
	return covered, nil
}

// HeldTotals returns the active held amount per currency.
func (s *WalletService) HeldTotals(ctx context.Context, workspaceID uuid.UUID) (map[string]int64, error) {
	w, err := s.Ensure(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.SumActiveWalletHolds(ctx, w.ID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Currency] = r.HeldMinor
	}
	return out, nil
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// CampaignHoldAmountMinor is the amount reserved for an active managed campaign.
func CampaignHoldAmountMinor(dailyBudget float64) int64 {
	if dailyBudget <= 0 {
		return 0
	}
	return int64(math.Round(dailyBudget * 100 * CampaignHoldDays))
}

// ListActiveHolds returns all active holds for a workspace.
func (s *WalletService) ListActiveHolds(ctx context.Context, workspaceID uuid.UUID) ([]db.WalletHold, error) {
	w, err := s.Ensure(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	var out []db.WalletHold
	for _, currency := range []string{CurrencyUSD, CurrencyNGN} {
		rows, err := s.queries.ListActiveWalletHolds(ctx, db.ListActiveWalletHoldsParams{
			WalletID: w.ID,
			Currency: currency,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}
