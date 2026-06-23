-- name: UpsertCampaign :one
INSERT INTO campaigns (workspace_id, ad_account_id, external_campaign_id, name, objective, status, daily_budget)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (ad_account_id, external_campaign_id)
DO UPDATE SET name = EXCLUDED.name, objective = EXCLUDED.objective, status = EXCLUDED.status, daily_budget = EXCLUDED.daily_budget, updated_at = now()
RETURNING *;

-- name: GetCampaignsByWorkspace :many
SELECT * FROM campaigns WHERE workspace_id = $1;
