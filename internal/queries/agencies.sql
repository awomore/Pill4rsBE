-- name: CreateAgency :one
INSERT INTO agencies (owner_user_id, name)
VALUES ($1, $2)
RETURNING *;

-- name: GetAgencyByID :one
SELECT * FROM agencies WHERE id = $1;

-- name: GetAgencyByWorkspace :one
SELECT a.* FROM agencies a
JOIN workspaces w ON w.agency_id = a.id
WHERE w.id = $1;

-- name: AddAgencyMember :one
INSERT INTO agency_members (agency_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (agency_id, user_id) DO UPDATE SET role = EXCLUDED.role
RETURNING *;

-- name: GetAgencyMember :one
SELECT * FROM agency_members WHERE agency_id = $1 AND user_id = $2;

-- name: ListAgencyMembers :many
SELECT m.id, m.agency_id, m.user_id, m.role, m.created_at, u.email
FROM agency_members m
JOIN users u ON u.id = m.user_id
WHERE m.agency_id = $1
ORDER BY m.created_at ASC;

-- name: RemoveAgencyMember :exec
DELETE FROM agency_members WHERE agency_id = $1 AND user_id = $2;

-- name: SetWorkspaceAgency :one
UPDATE workspaces SET agency_id = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: CreateAgencyWorkspace :one
INSERT INTO workspaces (user_id, agency_id, business_name, client_label)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListAgencyWorkspaces :many
SELECT * FROM workspaces WHERE agency_id = $1 ORDER BY created_at ASC;

-- name: GetAgencyWorkspace :one
SELECT * FROM workspaces WHERE id = $1 AND agency_id = $2;

-- name: GetAgencyTotals :one
SELECT
    COALESCE(SUM(ps.spend), 0)::numeric AS total_spend,
    COALESCE(SUM(ps.impressions), 0)::bigint AS total_impressions,
    COALESCE(SUM(ps.clicks), 0)::bigint AS total_clicks,
    COALESCE(SUM(ps.conversions), 0)::bigint AS total_conversions,
    COALESCE(AVG(ps.roas), 0)::numeric AS average_roas,
    COALESCE(AVG(ps.cpc), 0)::numeric AS average_cpc
FROM performance_snapshots ps
JOIN campaigns c ON ps.campaign_id = c.id
JOIN workspaces w ON c.workspace_id = w.id
WHERE w.agency_id = $1 AND ps.date >= $2 AND ps.date <= $3;

-- name: GetAgencyClientBreakdown :many
SELECT
    w.id AS workspace_id,
    COALESCE(NULLIF(w.client_label, ''), NULLIF(w.business_name, ''), 'Client') AS name,
    COALESCE(SUM(ps.spend), 0)::numeric AS spend,
    COALESCE(SUM(ps.impressions), 0)::bigint AS impressions,
    COALESCE(SUM(ps.clicks), 0)::bigint AS clicks,
    COALESCE(SUM(ps.conversions), 0)::bigint AS conversions,
    COUNT(DISTINCT c.id)::bigint AS campaigns,
    COUNT(DISTINCT c.id) FILTER (WHERE c.status = 'ACTIVE')::bigint AS active_campaigns
FROM workspaces w
LEFT JOIN campaigns c ON c.workspace_id = w.id
LEFT JOIN performance_snapshots ps
    ON ps.campaign_id = c.id AND ps.date >= $2 AND ps.date <= $3
WHERE w.agency_id = $1
GROUP BY w.id, w.client_label, w.business_name
ORDER BY spend DESC;

-- name: GetAgencyWalletTotals :many
SELECT
    wb.currency,
    COALESCE(SUM(wb.balance_minor), 0)::bigint AS available_minor,
    COALESCE(SUM(h.held_minor), 0)::bigint AS held_minor
FROM wallets wl
JOIN workspaces w ON wl.workspace_id = w.id
JOIN wallet_balances wb ON wb.wallet_id = wl.id
LEFT JOIN (
    SELECT wallet_id, currency, SUM(remaining_minor)::bigint AS held_minor
    FROM wallet_holds
    WHERE status = 'active'
    GROUP BY wallet_id, currency
) h ON h.wallet_id = wl.id AND h.currency = wb.currency
WHERE w.agency_id = $1
GROUP BY wb.currency;

-- name: GetAgencyCommissionTotals :many
SELECT
    co.currency,
    COALESCE(SUM(co.base_minor), 0)::bigint AS base_minor,
    COALESCE(SUM(co.commission_minor), 0)::bigint AS commission_minor
FROM commissions co
JOIN workspaces w ON co.workspace_id = w.id
WHERE w.agency_id = $1 AND co.created_at::date >= $2 AND co.created_at::date <= $3
GROUP BY co.currency;

-- name: GetAgencyCampaignCounts :one
SELECT
    COUNT(*)::bigint AS total_campaigns,
    COUNT(*) FILTER (WHERE c.status = 'ACTIVE')::bigint AS active_campaigns
FROM campaigns c
JOIN workspaces w ON c.workspace_id = w.id
WHERE w.agency_id = $1;
