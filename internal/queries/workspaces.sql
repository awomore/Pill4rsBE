-- name: CreateWorkspace :one
INSERT INTO workspaces (user_id)
VALUES ($1)
RETURNING *;

-- name: GetWorkspaceByUserID :one
SELECT * FROM workspaces WHERE user_id = $1;

-- name: UpdateWorkspaceProfile :one
UPDATE workspaces
SET business_name = $2, industry = $3, monthly_budget = $4, primary_goal = $5, target_audience = $6, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetOnboardingComplete :one
UPDATE workspaces
SET onboarding_complete = true, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetWorkspaceByID :one
SELECT * FROM workspaces WHERE id = $1;

-- name: SetWorkspaceSpendState :one
UPDATE workspaces
SET spend_state = $2, spend_state_reason = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetWorkspaceCommissionRate :one
UPDATE workspaces
SET commission_rate_bps = $2, updated_at = now()
WHERE id = $1
RETURNING *;
