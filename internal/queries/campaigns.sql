-- name: UpsertCampaign :one
INSERT INTO campaigns (workspace_id, ad_account_id, external_campaign_id, name, objective, status, daily_budget)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (ad_account_id, external_campaign_id)
DO UPDATE SET name = EXCLUDED.name, objective = EXCLUDED.objective, status = EXCLUDED.status, daily_budget = EXCLUDED.daily_budget, updated_at = now()
RETURNING *;

-- name: GetCampaignsByWorkspace :many
SELECT * FROM campaigns WHERE workspace_id = $1;

-- name: GetCampaignsWithLatestSnapshot :many
SELECT
    c.id, c.workspace_id, c.ad_account_id, c.external_campaign_id, c.name, c.objective, c.status, c.daily_budget, c.created_at, c.updated_at,
    ps.id AS snapshot_id, ps.date AS snapshot_date, ps.spend, ps.impressions, ps.clicks, ps.conversions, ps.reach, ps.cpm, ps.cpc, ps.ctr, ps.roas, ps.currency
FROM campaigns c
LEFT JOIN LATERAL (
    SELECT id, date, spend, impressions, clicks, conversions, reach, cpm, cpc, ctr, roas, currency
    FROM performance_snapshots
    WHERE campaign_id = c.id
    ORDER BY date DESC
    LIMIT 1
) ps ON true
WHERE c.workspace_id = $1
ORDER BY c.created_at DESC;

-- name: CreateCampaign :one
INSERT INTO campaigns (workspace_id, ad_account_id, external_campaign_id, name, objective, status, daily_budget, start_date, end_date, cta, targeting, external_adset_id, external_ad_id, external_creative_id, creative, bid_strategy, bid_cap, pacing_type, frequency_cap, frequency_cap_time_unit, variants, provenance, rationale)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
RETURNING *;

-- name: UpdateCampaignProvenance :one
UPDATE campaigns SET provenance = $2, updated_at = now() WHERE id = $1
RETURNING *;

-- name: UpdateCampaignRationale :one
UPDATE campaigns SET rationale = $2, updated_at = now() WHERE id = $1
RETURNING *;

-- name: GetCampaignByIDForWorkspace :one
SELECT * FROM campaigns WHERE id = $1 AND workspace_id = $2;

-- name: UpdateCampaignDeliverable :one
UPDATE campaigns
SET external_campaign_id = $2, external_adset_id = $3, external_creative_id = $4, external_ad_id = $5, status = $6, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateCampaignStatus :one
UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1
RETURNING *;

-- name: UpdateCampaignDailyBudget :one
UPDATE campaigns SET daily_budget = $2, updated_at = now() WHERE id = $1
RETURNING *;
