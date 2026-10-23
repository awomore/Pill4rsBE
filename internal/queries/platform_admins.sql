-- name: IsPlatformAdmin :one
SELECT EXISTS(SELECT 1 FROM platform_admins WHERE user_id = $1);

-- name: UpsertPlatformAdmin :one
INSERT INTO platform_admins (user_id, granted_by)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET granted_by = EXCLUDED.granted_by
RETURNING *;

-- name: DeletePlatformAdmin :exec
DELETE FROM platform_admins WHERE user_id = $1;

-- name: ListPlatformAdmins :many
SELECT p.user_id, p.granted_by, p.created_at, u.email
FROM platform_admins p
JOIN users u ON u.id = p.user_id
ORDER BY p.created_at ASC;

-- name: InsertAdminAuditLog :one
INSERT INTO admin_audit_logs (admin_user_id, action, target_type, target_id, metadata)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListAdminAuditLogs :many
SELECT l.id, l.admin_user_id, l.action, l.target_type, l.target_id, l.metadata, l.created_at, u.email AS admin_email
FROM admin_audit_logs l
JOIN users u ON u.id = l.admin_user_id
ORDER BY l.created_at DESC
LIMIT $1 OFFSET $2;

-- name: GetAdminOverview :one
SELECT
    (SELECT COUNT(*) FROM users)::bigint AS users,
    (SELECT COUNT(*) FROM agencies)::bigint AS agencies,
    (SELECT COUNT(*) FROM workspaces)::bigint AS workspaces,
    (SELECT COUNT(*) FROM campaigns)::bigint AS campaigns,
    (SELECT COUNT(*) FROM campaigns WHERE status = 'ACTIVE')::bigint AS active_campaigns,
    (SELECT COUNT(*) FROM ad_accounts)::bigint AS ad_accounts;

-- name: GetAdminWalletTotals :many
SELECT
    wb.currency,
    COALESCE(SUM(wb.balance_minor), 0)::bigint AS available_minor,
    COALESCE(SUM(h.held_minor), 0)::bigint AS held_minor
FROM wallet_balances wb
LEFT JOIN (
    SELECT wallet_id, currency, SUM(remaining_minor)::bigint AS held_minor
    FROM wallet_holds
    WHERE status = 'active'
    GROUP BY wallet_id, currency
) h ON h.wallet_id = wb.wallet_id AND h.currency = wb.currency
GROUP BY wb.currency;

-- name: ListAdminAgencies :many
SELECT
    a.id, a.name, a.created_at, u.email AS owner_email,
    (SELECT COUNT(*) FROM workspaces w WHERE w.agency_id = a.id)::bigint AS clients,
    (SELECT COUNT(*) FROM agency_members m WHERE m.agency_id = a.id)::bigint AS members
FROM agencies a
JOIN users u ON u.id = a.owner_user_id
ORDER BY a.created_at DESC;

-- name: ListAdminUsers :many
SELECT
    u.id, u.email, u.created_at,
    (SELECT COUNT(*) FROM agency_members m WHERE m.user_id = u.id)::bigint AS agencies,
    (SELECT COUNT(*) FROM workspaces w WHERE w.user_id = u.id)::bigint AS owned_workspaces
FROM users u
ORDER BY u.created_at DESC;
