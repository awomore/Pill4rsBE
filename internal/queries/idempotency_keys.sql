-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys
WHERE workspace_id = $1 AND endpoint = $2 AND key = $3;

-- name: InsertIdempotencyKey :execrows
INSERT INTO idempotency_keys (workspace_id, endpoint, key, request_hash)
VALUES ($1, $2, $3, $4)
ON CONFLICT (workspace_id, endpoint, key) DO NOTHING;

-- name: CompleteIdempotencyKey :exec
UPDATE idempotency_keys
SET status = 'completed', response_status = $4, response_body = $5, updated_at = now()
WHERE workspace_id = $1 AND endpoint = $2 AND key = $3;

-- name: DeleteIdempotencyKey :exec
DELETE FROM idempotency_keys
WHERE workspace_id = $1 AND endpoint = $2 AND key = $3;

-- name: DeleteExpiredIdempotencyKeys :exec
DELETE FROM idempotency_keys WHERE created_at < $1;
