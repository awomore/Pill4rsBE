-- name: CreateAdAccount :one
INSERT INTO ad_accounts (workspace_id, platform, external_account_id, access_token_encrypted, refresh_token_encrypted, token_expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpsertAdAccount :one
INSERT INTO ad_accounts (workspace_id, platform, external_account_id, access_token_encrypted, refresh_token_encrypted, token_expires_at, status, connected_at)
VALUES ($1, $2, $3, $4, $5, $6, 'active', now())
ON CONFLICT (workspace_id, platform) DO UPDATE
SET external_account_id = EXCLUDED.external_account_id,
    access_token_encrypted = EXCLUDED.access_token_encrypted,
    refresh_token_encrypted = EXCLUDED.refresh_token_encrypted,
    token_expires_at = EXCLUDED.token_expires_at,
    status = 'active',
    connected_at = now()
RETURNING *;

-- name: GetAdAccountsByWorkspace :many
SELECT * FROM ad_accounts WHERE workspace_id = $1 ORDER BY connected_at DESC;

-- name: GetAdAccountByID :one
SELECT * FROM ad_accounts WHERE id = $1;

-- name: SetAdAccountPageAndPixel :one
UPDATE ad_accounts SET page_id = $2, pixel_id = $3 WHERE id = $1
RETURNING *;

-- name: DeleteAdAccount :exec
DELETE FROM ad_accounts WHERE id = $1;

-- name: DeleteAdAccountByWorkspaceAndPlatform :exec
DELETE FROM ad_accounts WHERE workspace_id = $1 AND platform = $2;
