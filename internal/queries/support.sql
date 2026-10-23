-- name: CreateSupportTicket :one
INSERT INTO support_tickets (workspace_id, created_by, subject, priority)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetSupportTicket :one
SELECT * FROM support_tickets WHERE id = $1;

-- name: ListSupportTicketsByWorkspace :many
SELECT * FROM support_tickets
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListSupportTickets :many
SELECT * FROM support_tickets
WHERE ($1 = '' OR status = $1)
  AND ($2 = '' OR priority = $2)
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: UpdateSupportTicket :one
UPDATE support_tickets
SET status = $2, priority = $3, assigned_admin_id = $4, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: TouchSupportTicket :exec
UPDATE support_tickets SET updated_at = now() WHERE id = $1;

-- name: CreateSupportMessage :one
INSERT INTO support_messages (ticket_id, author_user_id, author_type, body, internal)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListSupportMessages :many
SELECT * FROM support_messages
WHERE ticket_id = $1 AND ($2 = true OR internal = false)
ORDER BY created_at ASC;

-- name: GetSupportStatusCounts :many
SELECT status, COUNT(*)::bigint AS count
FROM support_tickets
GROUP BY status;
