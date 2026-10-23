-- name: InsertWalletHold :one
INSERT INTO wallet_holds (wallet_id, workspace_id, currency, amount_minor, remaining_minor, status, source_type, source_id, idempotency_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (wallet_id, idempotency_key) DO UPDATE SET updated_at = now()
RETURNING *;

-- name: GetWalletHoldByIdempotency :one
SELECT * FROM wallet_holds WHERE wallet_id = $1 AND idempotency_key = $2;

-- name: GetActiveWalletHoldForSource :one
SELECT * FROM wallet_holds
WHERE wallet_id = $1 AND source_type = $2 AND source_id = $3 AND status = 'active'
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE;

-- name: ListActiveWalletHolds :many
SELECT * FROM wallet_holds
WHERE wallet_id = $1 AND currency = $2 AND status = 'active'
ORDER BY created_at ASC
FOR UPDATE;

-- name: SumActiveWalletHolds :many
SELECT currency, COALESCE(SUM(remaining_minor), 0)::bigint AS held_minor
FROM wallet_holds
WHERE wallet_id = $1 AND status = 'active'
GROUP BY currency;

-- name: UpdateWalletHoldRemaining :one
UPDATE wallet_holds
SET remaining_minor = $2, status = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ReactivateWalletHold :one
UPDATE wallet_holds
SET remaining_minor = $2, status = 'active', version = version + 1, updated_at = now()
WHERE id = $1
RETURNING *;
