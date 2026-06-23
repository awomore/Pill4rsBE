-- name: CreateAdAccount :one
INSERT INTO ad_accounts (workspace_id, platform, external_account_id, access_token_encrypted, refresh_token_encrypted, token_expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetAdAccountsByWorkspace :many
SELECT * FROM ad_accounts WHERE workspace_id = $1;

-- name: GetAdAccountByID :one
SELECT * FROM ad_accounts WHERE id = $1;

-- name: DeleteAdAccount :exec
DELETE FROM ad_accounts WHERE id = $1;
