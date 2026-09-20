-- name: EnsureWallet :one
INSERT INTO wallets (workspace_id)
VALUES ($1)
ON CONFLICT (workspace_id) DO UPDATE SET updated_at = now()
RETURNING *;

-- name: GetWalletByWorkspace :one
SELECT * FROM wallets WHERE workspace_id = $1;

-- name: EnsureWalletBalance :one
INSERT INTO wallet_balances (wallet_id, currency)
VALUES ($1, $2)
ON CONFLICT (wallet_id, currency) DO UPDATE SET updated_at = now()
RETURNING *;

-- name: GetWalletBalance :one
SELECT * FROM wallet_balances WHERE wallet_id = $1 AND currency = $2;

-- name: ListWalletBalances :many
SELECT * FROM wallet_balances WHERE wallet_id = $1 ORDER BY currency;

-- name: AddWalletBalance :one
UPDATE wallet_balances
SET balance_minor = balance_minor + $3, updated_at = now()
WHERE wallet_id = $1 AND currency = $2
RETURNING *;

-- name: InsertWalletTransaction :one
INSERT INTO wallet_transactions (wallet_id, currency, kind, amount_minor, balance_after_minor, idempotency_key, source_type, source_id, fx_rate, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (wallet_id, idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
RETURNING *;

-- name: GetWalletTransactionByIdempotency :one
SELECT * FROM wallet_transactions WHERE wallet_id = $1 AND idempotency_key = $2;

-- name: ListWalletTransactions :many
SELECT * FROM wallet_transactions
WHERE wallet_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: EnsureBillingProfile :one
INSERT INTO billing_profiles (workspace_id)
VALUES ($1)
ON CONFLICT (workspace_id) DO UPDATE SET updated_at = now()
RETURNING *;

-- name: GetBillingProfileByWorkspace :one
SELECT * FROM billing_profiles WHERE workspace_id = $1;

-- name: SetBillingProfileProvider :one
UPDATE billing_profiles
SET provider_customer_id = $2, default_payment_method = $3, billing_mode = $4, updated_at = now()
WHERE workspace_id = $1
RETURNING *;

-- name: InsertProviderEvent :one
INSERT INTO provider_events (event_id, type)
VALUES ($1, $2)
ON CONFLICT (event_id) DO UPDATE SET event_id = EXCLUDED.event_id
RETURNING *;

-- name: GetProviderEvent :one
SELECT * FROM provider_events WHERE event_id = $1;
