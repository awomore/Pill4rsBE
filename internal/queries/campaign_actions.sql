-- name: CreateCampaignAction :one
INSERT INTO campaign_actions (workspace_id, actor, type, payload)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetCampaignActionByIDForWorkspace :one
SELECT * FROM campaign_actions WHERE id = $1 AND workspace_id = $2;

-- name: ListCampaignActionsByWorkspace :many
SELECT * FROM campaign_actions WHERE workspace_id = $1 ORDER BY created_at DESC;

-- name: UpdateCampaignActionResult :one
UPDATE campaign_actions
SET status = $2, result = $3, error = $4, updated_at = now()
WHERE id = $1
RETURNING *;
