-- name: CreateMediaAsset :one
INSERT INTO media_assets (workspace_id, kind, storage_key, public_url, mime, bytes, width, height, duration_ms, checksum, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ListMediaAssets :many
SELECT * FROM media_assets
WHERE workspace_id = $1
ORDER BY created_at DESC;

-- name: GetMediaAsset :one
SELECT * FROM media_assets WHERE id = $1 AND workspace_id = $2;

-- name: DeleteMediaAsset :exec
DELETE FROM media_assets WHERE id = $1 AND workspace_id = $2;

-- name: SetMediaAssetPlatformHash :one
UPDATE media_assets
SET platform_hashes = $2, updated_at = now()
WHERE id = $1
RETURNING *;
