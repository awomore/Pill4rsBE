-- name: CreateSession :one
INSERT INTO sessions (user_id, workspace_id, refresh_token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: SetSessionWorkspaceByHash :exec
UPDATE sessions SET workspace_id = $2 WHERE refresh_token_hash = $1;

-- name: GetSessionByRefreshHash :one
SELECT * FROM sessions WHERE refresh_token_hash = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = $1;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < now();

-- name: ListSessionsByUser :many
SELECT * FROM sessions WHERE user_id = $1 ORDER BY created_at DESC;

-- name: DeleteSessionForUser :exec
DELETE FROM sessions WHERE id = $1 AND user_id = $2;
