-- name: CreateAPIKey :one
INSERT INTO api_keys (workspace_id, agency_id, name, prefix, key_hash, scopes)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListAPIKeysByWorkspace :many
SELECT * FROM api_keys WHERE workspace_id = $1 ORDER BY created_at DESC;

-- name: GetAPIKeyByHash :one
SELECT * FROM api_keys WHERE key_hash = $1 AND revoked_at IS NULL;

-- name: RevokeAPIKey :exec
UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND workspace_id = $2 AND revoked_at IS NULL;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = now() WHERE id = $1;
