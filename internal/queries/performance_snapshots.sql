-- name: UpsertPerformanceSnapshot :one
INSERT INTO performance_snapshots (campaign_id, date, spend, impressions, clicks, conversions, reach, cpm, cpc, ctr, roas, currency)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (campaign_id, date)
DO UPDATE SET spend = EXCLUDED.spend, impressions = EXCLUDED.impressions, clicks = EXCLUDED.clicks, conversions = EXCLUDED.conversions, reach = EXCLUDED.reach, cpm = EXCLUDED.cpm, cpc = EXCLUDED.cpc, ctr = EXCLUDED.ctr, roas = EXCLUDED.roas, currency = EXCLUDED.currency
RETURNING *;

-- name: GetSnapshotsByCampaignAndDateRange :many
SELECT * FROM performance_snapshots
WHERE campaign_id = $1 AND date >= $2 AND date <= $3
ORDER BY date;

-- name: GetAggregatedSummaryByWorkspace :one
SELECT
    SUM(ps.spend)::numeric AS total_spend,
    SUM(ps.impressions)::bigint AS total_impressions,
    SUM(ps.clicks)::bigint AS total_clicks,
    SUM(ps.conversions)::bigint AS total_conversions,
    SUM(ps.reach)::bigint AS total_reach,
    AVG(ps.cpm)::numeric AS avg_cpm,
    AVG(ps.cpc)::numeric AS avg_cpc,
    AVG(ps.ctr)::numeric AS avg_ctr,
    AVG(ps.roas)::numeric AS avg_roas
FROM performance_snapshots ps
JOIN campaigns c ON ps.campaign_id = c.id
WHERE c.workspace_id = $1 AND ps.date >= $2 AND ps.date <= $3;

-- name: GetDashboardTotals :one
SELECT
    COALESCE(SUM(ps.spend), 0)::numeric AS total_spend,
    COALESCE(SUM(ps.impressions), 0)::bigint AS total_impressions,
    COALESCE(SUM(ps.clicks), 0)::bigint AS total_clicks,
    COALESCE(SUM(ps.conversions), 0)::bigint AS total_conversions,
    COALESCE(AVG(ps.roas), 0)::numeric AS average_roas,
    COALESCE(AVG(ps.cpc), 0)::numeric AS average_cpc
FROM performance_snapshots ps
JOIN campaigns c ON ps.campaign_id = c.id
WHERE c.workspace_id = $1 AND ps.date >= $2 AND ps.date <= $3;

-- name: GetDashboardDailySeries :many
SELECT
    ps.date AS date,
    COALESCE(SUM(ps.spend), 0)::numeric AS spend,
    COALESCE(SUM(ps.conversions), 0)::bigint AS conversions
FROM performance_snapshots ps
JOIN campaigns c ON ps.campaign_id = c.id
WHERE c.workspace_id = $1 AND ps.date >= $2 AND ps.date <= $3
GROUP BY ps.date
ORDER BY ps.date;

-- name: GetDashboardCampaignBreakdown :many
SELECT
    c.id AS campaign_id,
    c.name AS name,
    c.status AS status,
    COALESCE(SUM(ps.spend), 0)::numeric AS spend,
    COALESCE(SUM(ps.impressions), 0)::bigint AS impressions,
    COALESCE(SUM(ps.clicks), 0)::bigint AS clicks,
    COALESCE(SUM(ps.conversions), 0)::bigint AS conversions,
    COALESCE(AVG(ps.roas), 0)::numeric AS average_roas,
    COALESCE(AVG(ps.cpc), 0)::numeric AS average_cpc
FROM campaigns c
LEFT JOIN performance_snapshots ps
    ON ps.campaign_id = c.id AND ps.date >= $2 AND ps.date <= $3
WHERE c.workspace_id = $1
GROUP BY c.id, c.name, c.status
ORDER BY spend DESC;
