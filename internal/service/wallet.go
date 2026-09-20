package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Supported wallet currencies. Global launch supports USD and NGN.
const (
	CurrencyUSD = "USD"
	CurrencyNGN = "NGN"
)

// Ledger transaction kinds.
const (
	TxKindTopUp      = "topup"
	TxKindMediaSpend = "media_spend"
	TxKindCommission = "commission"
	TxKindAdjustment = "adjustment"
	TxKindRefund     = "refund"
	TxKindFX         = "fx"
)

// ErrInsufficientBalance is returned when a debit would take a wallet negative.
var ErrInsufficientBalance = errors.New("insufficient wallet balance")

// WalletService owns the prepaid wallet and its append-only ledger. Every
// mutation goes through Apply, which is atomic and idempotent.
type WalletService struct {
	pool    *pgxpool.Pool
	queries *db.Queries
	onDebit func(ctx context.Context, workspaceID uuid.UUID)
}

func NewWalletService(pool *pgxpool.Pool, queries *db.Queries) *WalletService {
	return &WalletService{pool: pool, queries: queries}
}

// SetOnDebit registers a hook invoked after any successful debit, so the spend
// guard can hard-pause a workspace the moment its balance is exhausted.
func (s *WalletService) SetOnDebit(fn func(ctx context.Context, workspaceID uuid.UUID)) {
	s.onDebit = fn
}

// CurrencyBalance is one currency's available balance in minor units.
type CurrencyBalance struct {
	Currency     string `json:"currency"`
	BalanceMinor int64  `json:"balance_minor"`
}

// WalletSummary is the wallet plus every currency balance.
type WalletSummary struct {
	WalletID uuid.UUID         `json:"wallet_id"`
	Status   string            `json:"status"`
	Balances []CurrencyBalance `json:"balances"`
}

// Ensure creates the wallet (and USD/NGN balances) if missing.
func (s *WalletService) Ensure(ctx context.Context, workspaceID uuid.UUID) (db.Wallet, error) {
	w, err := s.queries.EnsureWallet(ctx, pgUUID(workspaceID))
	if err != nil {
		return db.Wallet{}, err
	}
	for _, c := range []string{CurrencyUSD, CurrencyNGN} {
		if _, err := s.queries.EnsureWalletBalance(ctx, db.EnsureWalletBalanceParams{
			WalletID: w.ID,
			Currency: c,
		}); err != nil {
			return w, err
		}
	}
	return w, nil
}

// Summary returns the wallet and all balances, creating them if needed.
func (s *WalletService) Summary(ctx context.Context, workspaceID uuid.UUID) (WalletSummary, error) {
	w, err := s.Ensure(ctx, workspaceID)
	if err != nil {
		return WalletSummary{}, err
	}
	balances, err := s.queries.ListWalletBalances(ctx, w.ID)
	if err != nil {
		return WalletSummary{}, err
	}
	out := WalletSummary{WalletID: uuidFromPG(w.ID), Status: w.Status}
	for _, b := range balances {
		out.Balances = append(out.Balances, CurrencyBalance{Currency: b.Currency, BalanceMinor: b.BalanceMinor})
	}
	return out, nil
}

// LedgerEntry is one wallet mutation. AmountMinor is signed: positive credits,
// negative debits. IdempotencyKey deduplicates retries (unique per wallet).
type LedgerEntry struct {
	WorkspaceID    uuid.UUID
	Currency       string
	Kind           string
	AmountMinor    int64
	IdempotencyKey string
	SourceType     string
	SourceID       string
	FxRate         *float64
	Metadata       map[string]any
}

// Apply writes a ledger entry and moves the balance atomically. Replaying the
// same IdempotencyKey returns the original transaction without moving money.
func (s *WalletService) Apply(ctx context.Context, e LedgerEntry) (db.WalletTransaction, error) {
	if e.IdempotencyKey == "" {
		return db.WalletTransaction{}, fmt.Errorf("idempotency key is required")
	}
	currency, err := normalizeCurrency(e.Currency)
	if err != nil {
		return db.WalletTransaction{}, err
	}
	e.Currency = currency

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.WalletTransaction{}, fmt.Errorf("begin wallet tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := s.queries.WithTx(tx)
	w, err := q.EnsureWallet(ctx, pgUUID(e.WorkspaceID))
	if err != nil {
		return db.WalletTransaction{}, fmt.Errorf("ensure wallet: %w", err)
	}

	existing, err := q.GetWalletTransactionByIdempotency(ctx, db.GetWalletTransactionByIdempotencyParams{
		WalletID:       w.ID,
		IdempotencyKey: e.IdempotencyKey,
	})
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.WalletTransaction{}, fmt.Errorf("check idempotency: %w", err)
	}

	if _, err := q.EnsureWalletBalance(ctx, db.EnsureWalletBalanceParams{
		WalletID: w.ID,
		Currency: e.Currency,
	}); err != nil {
		return db.WalletTransaction{}, fmt.Errorf("ensure balance: %w", err)
	}

	bal, err := q.AddWalletBalance(ctx, db.AddWalletBalanceParams{
		WalletID:     w.ID,
		Currency:     e.Currency,
		BalanceMinor: e.AmountMinor,
	})
	if err != nil {
		return db.WalletTransaction{}, fmt.Errorf("move balance: %w", err)
	}

	var meta []byte
	if len(e.Metadata) > 0 {
		meta, _ = json.Marshal(e.Metadata)
	}

	t, err := q.InsertWalletTransaction(ctx, db.InsertWalletTransactionParams{
		WalletID:          w.ID,
		Currency:          e.Currency,
		Kind:              e.Kind,
		AmountMinor:       e.AmountMinor,
		BalanceAfterMinor: bal.BalanceMinor,
		IdempotencyKey:    e.IdempotencyKey,
		SourceType:        textOrNull(e.SourceType),
		SourceID:          textOrNull(e.SourceID),
		FxRate:            numericOrNullPtr(e.FxRate),
		Metadata:          meta,
	})
	if err != nil {
		return db.WalletTransaction{}, fmt.Errorf("insert ledger entry: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return db.WalletTransaction{}, fmt.Errorf("commit wallet tx: %w", err)
	}
	return t, nil
}

// Credit adds funds (top-up, refund, adjustment).
func (s *WalletService) Credit(ctx context.Context, e LedgerEntry) (db.WalletTransaction, error) {
	if e.AmountMinor <= 0 {
		return db.WalletTransaction{}, ValidationError{"credit amount must be positive"}
	}
	return s.Apply(ctx, e)
}

// Debit removes funds. It triggers the on-debit hook so the spend guard can
// hard-pause a workspace that has run out of balance.
func (s *WalletService) Debit(ctx context.Context, e LedgerEntry) (db.WalletTransaction, error) {
	if e.AmountMinor > 0 {
		e.AmountMinor = -e.AmountMinor
	}
	t, err := s.Apply(ctx, e)
	if err != nil {
		return t, err
	}
	if s.onDebit != nil {
		s.onDebit(ctx, e.WorkspaceID)
	}
	return t, nil
}

// TopUp credits the wallet from a payment provider reference. The reference is
// the idempotency key, so webhook retries never double-credit.
func (s *WalletService) TopUp(ctx context.Context, workspaceID uuid.UUID, currency string, amountMinor int64, provider, reference string) (db.WalletTransaction, error) {
	if amountMinor <= 0 {
		return db.WalletTransaction{}, ValidationError{"amount must be positive"}
	}
	if strings.TrimSpace(reference) == "" {
		return db.WalletTransaction{}, ValidationError{"payment reference is required"}
	}
	return s.Credit(ctx, LedgerEntry{
		WorkspaceID:    workspaceID,
		Currency:       currency,
		Kind:           TxKindTopUp,
		AmountMinor:    amountMinor,
		IdempotencyKey: "topup:" + provider + ":" + reference,
		SourceType:     "topup",
		SourceID:       reference,
		Metadata:       map[string]any{"provider": provider},
	})
}

// ListTransactions returns the ledger newest-first.
func (s *WalletService) ListTransactions(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]db.WalletTransaction, error) {
	w, err := s.Ensure(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.queries.ListWalletTransactions(ctx, db.ListWalletTransactionsParams{
		WalletID: w.ID,
		Limit:    limit,
		Offset:   offset,
	})
}

// ProviderEventSeen reports whether a payment-provider webhook event was
// already handled, so retries are ignored.
func (s *WalletService) ProviderEventSeen(ctx context.Context, eventID string) bool {
	_, err := s.queries.GetProviderEvent(ctx, eventID)
	return err == nil
}

// RecordProviderEvent marks a payment-provider event as handled.
func (s *WalletService) RecordProviderEvent(ctx context.Context, eventID, eventType string) error {
	_, err := s.queries.InsertProviderEvent(ctx, db.InsertProviderEventParams{
		EventID: eventID,
		Type:    eventType,
	})
	return err
}

func normalizeCurrency(c string) (string, error) {
	c = strings.ToUpper(strings.TrimSpace(c))
	switch c {
	case CurrencyUSD, CurrencyNGN:
		return c, nil
	default:
		return "", ValidationError{"currency must be one of: USD, NGN"}
	}
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func uuidFromPG(id pgtype.UUID) uuid.UUID {
	return uuid.UUID(id.Bytes)
}

func numericOrNullPtr(f *float64) pgtype.Numeric {
	if f == nil {
		return pgtype.Numeric{}
	}
	return numericFromFloat(*f)
}
