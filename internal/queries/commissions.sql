-- name: GetCommissionBySnapshot :one
SELECT * FROM commissions WHERE snapshot_id = $1;

-- name: InsertCommission :one
INSERT INTO commissions (workspace_id, ad_account_id, snapshot_id, currency, base_minor, rate_bps, commission_minor, fx_rate)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (snapshot_id) DO UPDATE SET snapshot_id = EXCLUDED.snapshot_id
RETURNING *;

-- name: ListCommissionsByWorkspace :many
SELECT * FROM commissions
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2;
